package deploycli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jmeiracorbal/docker-blue-green/internal/config"
	"github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

var dotenvKey = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*$")

// appendSecretReferences is shared by secrets add and secrets import.
// Neither path bypasses the contract's duplicate and service validations.
func appendSecretReferences(cfg config.Document, mount string, refs []openbao.Reference) (config.Document, int, error) {
	if cfg.OpenBao == nil {
		cfg.OpenBao = &config.OpenBao{Mount: mount}
	} else {
		cloned := *cfg.OpenBao
		cloned.Secrets = append([]openbao.Reference(nil), cloned.Secrets...)
		cfg.OpenBao = &cloned
		if mount != cloned.Mount {
			return config.Document{}, 0, errors.New("OpenBao mount differs from deploy/bluegreen.yml")
		}
	}
	added := 0
	for _, ref := range refs {
		found := false
		for _, current := range cfg.OpenBao.Secrets {
			if current.Service == ref.Service && current.Name == ref.Name {
				if current != ref {
					return config.Document{}, 0, errors.New("OpenBao reference already exists with different settings")
				}
				found = true
				break
			}
		}
		if !found {
			cfg.OpenBao.Secrets = append(cfg.OpenBao.Secrets, ref)
			added++
		}
	}
	if err := cfg.Validate(false); err != nil { return config.Document{}, 0, err }
	return cfg, added, nil
}

func importSecrets(args []string, input io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("secrets import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", ".", "project directory")
	project := flags.String("project", "", "optional project confirmation")
	mount := flags.String("mount", "", "existing KV v2 mount")
	service := flags.String("service", "", "application Compose service")
	envFile := flags.String("env-file", "", "explicit local dotenv file")
	update := flags.Bool("update", false, "authorize changes to existing KV v2 fields")
	if err := flags.Parse(args); err != nil { return err }
	if flags.NArg() != 0 || *envFile == "" || *service == "" {
		return errors.New("usage: bluegreen secrets import --env-file FILE --service SERVICE [--mount KV] [--update] [--project NAME] [--dir PATH]")
	}
	cfg, err := config.Load(*dir)
	if err != nil { return err }
	if err := matchProject(cfg, *project); err != nil { return err }
	effectiveMount, err := configuredMount(cfg, *mount)
	if err != nil { return err }
	values, names, err := readDotenv(*envFile)
	if err != nil { return err }
	refs := make([]openbao.Reference, 0, len(names))
	for _, name := range names {
		refs = append(refs, openbao.Reference{
			Service: *service, Name: strings.ToLower(name),
			Path: "env/" + *service, Field: name,
		})
	}
	next, added, err := appendSecretReferences(cfg, effectiveMount, refs)
	if err != nil { return err }
	if err := next.ValidateApplicationServices(*dir); err != nil { return err }
	if _, err := os.Stat(config.Path(*dir)); err != nil { return err }

	if _, err := fmt.Fprintf(out, "Project: %s\nService: %s\nOpenBao: %s/data/projects/%s/env/%s\nFields (%d):\n",
		cfg.Project, *service, effectiveMount, cfg.Project, *service, len(names)); err != nil { return err }
	for _, name := range names {
		if _, err := fmt.Fprintln(out, name); err != nil { return err }
	}
	if *update {
		if _, err := fmt.Fprintln(out, "Existing fields may be updated (CAS enabled)."); err != nil { return err }
	}
	if _, err := fmt.Fprint(out, "Transmit these fields to OpenBao? Type yes to confirm: "); err != nil { return err }
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && answer != "") {
		return errors.New("import cancelled: confirmation not received")
	}
	if strings.TrimSpace(answer) != "yes" {
		return errors.New("import cancelled")
	}
	writer, err := openbao.NewWriter()
	if err != nil { return err }
	written, err := writer.PutFields(context.Background(), cfg.Project, effectiveMount, "env/" + *service, values, *update)
	if err != nil { return err }
	if added > 0 {
		if err := config.Save(*dir, next); err != nil {
			return fmt.Errorf("OpenBao may contain the imported values, but the local contract could not be saved: %w", err)
		}
	}
	_, err = fmt.Fprintf(out, "OpenBao import complete: %d fields, %d new references, KV write: %t\n",
		len(names), added, written)
	return err
}

// readDotenv deliberately does not run a shell, expand variables or read
// implicit .env files. Only regular explicitly named files are accepted.
func readDotenv(path string) (map[string]string, []string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("dotenv input must be an existing regular file")
	}
	if info.Size() > 1<<20 {
		return nil, nil, errors.New("dotenv input exceeds 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil { return nil, nil, errors.New("cannot open dotenv input") }
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, (1<<20)+1))
	scanner.Buffer(make([]byte, 4096), (1<<20)+1)
	values := map[string]string{}
	normalized := map[string]bool{}
	line := 0
	for scanner.Scan() {
		line++
		entry := strings.TrimSpace(scanner.Text())
		if entry == "" || strings.HasPrefix(entry, "#") { continue }
		if strings.HasPrefix(entry, "export ") {
			entry = strings.TrimSpace(strings.TrimPrefix(entry, "export "))
		}
		key, raw, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || !dotenvKey.MatchString(key) {
			return nil, nil, fmt.Errorf("invalid dotenv assignment on line %d", line)
		}
		value, err := parseDotenvValue(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid dotenv value on line %d", line)
		}
		if _, exists := values[key]; exists || normalized[strings.ToLower(key)] {
			return nil, nil, fmt.Errorf("duplicate or colliding dotenv name on line %d", line)
		}
		values[key] = value
		normalized[strings.ToLower(key)] = true
		if len(values) > 512 {
			return nil, nil, errors.New("dotenv input exceeds 512 assignments")
		}
	}
	if err := scanner.Err(); err != nil { return nil, nil, errors.New("cannot parse dotenv input") }
	if len(values) == 0 { return nil, nil, errors.New("dotenv input contains no assignments") }
	names := make([]string, 0, len(values))
	for key := range values { names = append(names, key) }
	sort.Strings(names)
	return values, names, nil
}

func parseDotenvValue(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" { return "", errors.New("empty value") }
	var value string
	switch s[0] {
	case '"', '\'':
		quote := s[0]
		end := -1
		escaped := false
		for i := 1; i < len(s); i++ {
			if quote == '"' && s[i] == '\\' && !escaped {
				escaped = true
				continue
			}
			if s[i] == quote && !escaped {
				end = i
				break
			}
			escaped = false
		}
		if end < 0 { return "", errors.New("unterminated quote") }
		tail := strings.TrimSpace(s[end+1:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", errors.New("ambiguous quoted value suffix")
		}
		if quote == '"' {
			decoded, err := strconv.Unquote(s[:end+1])
			if err != nil { return "", errors.New("invalid quoted escape") }
			value = decoded
		} else {
			value = s[1:end]
		}
	default:
		for i := 0; i < len(s); i++ {
			if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				s = s[:i]
				break
			}
		}
		value = strings.TrimSpace(s)
		if strings.ContainsAny(value, "\"'") { return "", errors.New("ambiguous unquoted value") }
	}
	if value == "" || len(value) > 65536 || strings.ContainsRune(value, 0) ||
		strings.Contains(value, "${") || strings.Contains(value, "$(") ||
		strings.ContainsRune(value, '\x60') {
		return "", errors.New("empty, oversized or executable dotenv value")
	}
	return value, nil
}
