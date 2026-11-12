package scaffold

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type File struct {
	Path string
	Data []byte
}
type Options struct {
	Root, SourcePath                    string
	ComposeJSON                         []byte
	AppServices, InfrastructureServices []string
	Input                               io.Reader
	Output                              io.Writer
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z_0-9]*)\}`)
var interpolationPattern = regexp.MustCompile(`\$\{([^}]+)\}`)
var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*$`)
var namePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

func FindSource(root, requested string) (string, bool, error) {
	if requested != "" {
		path := requested
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", false, err
		}
		if info.IsDir() {
			return "", false, fmt.Errorf("Compose source is a directory: %s", path)
		}
		return path, true, nil
	}
	candidates := []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}
	var found []string
	for _, candidate := range candidates {
		path := filepath.Join(root, candidate)
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			found = append(found, path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	if len(found) > 1 {
		return "", false, fmt.Errorf("multiple Compose sources found; select one with --compose: %s", strings.Join(found, ", "))
	}
	if len(found) == 0 {
		return "", false, nil
	}
	return found[0], true, nil
}

func Generate(o Options) ([]File, error) {
	if o.Input == nil {
		o.Input = strings.NewReader("")
	}
	if o.Output == nil {
		o.Output = io.Discard
	}
	if len(o.ComposeJSON) == 0 {
		if len(o.AppServices) > 0 || len(o.InfrastructureServices) > 0 {
			return nil, errors.New("service classifications require an existing Compose source")
		}
		return append(emptyScaffold(), publishWorkflowFile()), nil
	}
	for _, match := range interpolationPattern.FindAllSubmatch(o.ComposeJSON, -1) {
		if !variableNamePattern.Match(match[1]) {
			return nil, fmt.Errorf("source contains non-simple Compose interpolation %q; only ${VARIABLE} is supported", string(match[0]))
		}
	}
	var source map[string]any
	dec := json.NewDecoder(bytes.NewReader(o.ComposeJSON))
	dec.UseNumber()
	if err := dec.Decode(&source); err != nil {
		return nil, fmt.Errorf("parse Compose model: %w", err)
	}
	services, ok := asMap(source["services"])
	if !ok || len(services) == 0 {
		return nil, errors.New("source Compose file has no services")
	}
	names := keys(services)
	classification, err := classify(names, o.AppServices, o.InfrastructureServices, o.Input, o.Output)
	if err != nil {
		return nil, err
	}
	if err := checkSharedVolumes(source, services, classification); err != nil {
		return nil, err
	}
	app := clone(source)
	infra := clone(source)
	appServices, _ := asMap(app["services"])
	infraServices, _ := asMap(infra["services"])
	for _, name := range names {
		if classification[name] == "app" {
			delete(infraServices, name)
		} else {
			delete(appServices, name)
		}
	}
	// The application and infrastructure are separate Compose projects.
	app["name"] = "${PROJECT_NAME}-${DEPLOYMENT_SLOT}"
	infra["name"] = "${PROJECT_NAME}-infrastructure"
	if err := adjustServices(appServices, infraServices, o.SourcePath, classification); err != nil {
		return nil, err
	}
	if err := prepareNetworks(app, infra, appServices, infraServices); err != nil {
		return nil, err
	}
	if err := prepareVolumes(app, infra, appServices, infraServices); err != nil {
		return nil, err
	}
	rebasePaths(app, o.SourcePath, filepath.Join(o.Root, "deploy"))
	rebasePaths(infra, o.SourcePath, filepath.Join(o.Root, "deploy", "infrastructure"))
	if _, exists := infraServices["caddy"]; !exists {
		infraServices["caddy"] = map[string]any{
			"image":       "${CADDY_IMAGE}",
			"environment": map[string]any{"CADDY_ACME_EMAIL": "${CADDY_ACME_EMAIL}"},
			"ports":       []any{"${HTTP_PORT}:80/tcp", "${HTTPS_PORT}:443/tcp", "${HTTPS_PORT}:443/udp"},
			"volumes":     []any{"./caddy/Caddyfile:/etc/caddy/Caddyfile:ro", "./caddy/routes:/etc/caddy/routes:ro", "caddy_data:/data", "caddy_config:/config"},
			"networks":    map[string]any{"bluegreen": nil}, "restart": "unless-stopped",
		}
		vols, _ := asMap(infra["volumes"])
		if vols == nil {
			vols = map[string]any{}
			infra["volumes"] = vols
		}
		if _, exists := vols["caddy_data"]; exists {
			return nil, errors.New("volume caddy_data already exists; cannot provision Caddy safely")
		}
		if _, exists := vols["caddy_config"]; exists {
			return nil, errors.New("volume caddy_config already exists; cannot provision Caddy safely")
		}
		vols["caddy_data"] = map[string]any{}
		vols["caddy_config"] = map[string]any{}
	}
	// The Caddy service must be present in the shared infrastructure.
	if _, exists := appServices["caddy"]; exists {
		return nil, errors.New("caddy must be classified as infrastructure")
	}
	// Remove fields that cannot be independent between Compose projects.
	for _, model := range []map[string]any{app, infra} {
		delete(model, "version")
	}
	appData := marshalYAML(app)
	infraData := marshalYAML(infra)

	files := []File{
		{Path: "deploy/routes.json", Data: []byte("{\n  \"routes\": []\n}\n")},
		{Path: "deploy/compose.yml", Data: appData},
		{Path: "deploy/infrastructure/compose.yml", Data: infraData},
		{Path: "deploy/infrastructure/caddy/Caddyfile", Data: []byte("{\n    email {$CADDY_ACME_EMAIL}\n}\n\nimport /etc/caddy/routes/*.caddy\n")},
		{Path: "deploy/infrastructure/caddy/routes/.gitkeep", Data: []byte{}},
	}
	vars := map[string]bool{"PROJECT_NAME": true, "DEPLOYMENT_SLOT": true}
	for _, file := range files {
		for _, match := range envPattern.FindAllSubmatch(file.Data, -1) {
			vars[string(match[1])] = true
		}
	}
	var env bytes.Buffer
	for _, name := range sortedKeys(vars) {
		fmt.Fprintf(&env, "%s=\n", name)
	}
	files = append(files, File{Path: ".env.example", Data: env.Bytes()})
	files = append(files, publishWorkflowFile())
	return files, nil
}

func emptyScaffold() []File {
	return []File{
		{Path: "deploy/routes.json", Data: []byte("{\n  \"routes\": []\n}\n")},
		{Path: "deploy/compose.yml", Data: []byte("name: \"${PROJECT_NAME}-${DEPLOYMENT_SLOT}\"\nservices:\n  app:\n    image: \"${APPLICATION_IMAGE}\"\n    expose:\n      - \"${APPLICATION_PORT}\"\n    networks:\n      bluegreen:\n        aliases:\n          - \"${PROJECT_NAME}-${DEPLOYMENT_SLOT}-app\"\nnetworks:\n  bluegreen:\n    external: true\n    name: \"${DEPLOY_NETWORK}\"\n")},
		{Path: "deploy/infrastructure/compose.yml", Data: []byte("name: \"${PROJECT_NAME}-infrastructure\"\nservices:\n  caddy:\n    image: \"${CADDY_IMAGE}\"\n    environment:\n      CADDY_ACME_EMAIL: \"${CADDY_ACME_EMAIL}\"\n    ports:\n      - \"${HTTP_PORT}:80/tcp\"\n      - \"${HTTPS_PORT}:443/tcp\"\n      - \"${HTTPS_PORT}:443/udp\"\n    volumes:\n      - ./caddy/Caddyfile:/etc/caddy/Caddyfile:ro\n      - ./caddy/routes:/etc/caddy/routes:ro\n      - caddy_data:/data\n      - caddy_config:/config\n    networks:\n      - bluegreen\nnetworks:\n  bluegreen:\n    name: \"${DEPLOY_NETWORK}\"\nvolumes:\n  caddy_data: {}\n  caddy_config: {}\n")},
		{Path: "deploy/infrastructure/caddy/Caddyfile", Data: []byte("{\n    email {$CADDY_ACME_EMAIL}\n}\n\nimport /etc/caddy/routes/*.caddy\n")},
		{Path: "deploy/infrastructure/caddy/routes/.gitkeep", Data: []byte{}},
		{Path: ".env.example", Data: []byte("APPLICATION_IMAGE=\nAPPLICATION_PORT=\nCADDY_ACME_EMAIL=\nCADDY_IMAGE=\nDEPLOYMENT_SLOT=\nDEPLOY_NETWORK=\nHTTPS_PORT=\nHTTP_PORT=\nPROJECT_NAME=\n")},
	}
}

func classify(names, apps, infra []string, in io.Reader, out io.Writer) (map[string]string, error) {
	result := map[string]string{}
	for _, entry := range []struct {
		list []string
		kind string
	}{{apps, "app"}, {infra, "infra"}} {
		for _, name := range entry.list {
			if !slices.Contains(names, name) {
				return nil, fmt.Errorf("unknown service %q", name)
			}
			if prev, exists := result[name]; exists {
				return nil, fmt.Errorf("service %q classified as both %s and %s", name, prev, entry.kind)
			}
			result[name] = entry.kind
		}
	}
	reader := bufio.NewReader(in)
	for _, name := range names {
		if _, ok := result[name]; ok {
			continue
		}
		fmt.Fprintf(out, "Service %s: [a]pplication or [i]nfrastructure? ", name)
		answer, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "a", "app", "application":
			result[name] = "app"
		case "i", "infra", "infrastructure":
			result[name] = "infra"
		default:
			return nil, fmt.Errorf("service %q must be classified explicitly (use --app and --infrastructure for non-interactive use)", name)
		}
	}
	if !slices.Contains(mapsValues(result), "app") {
		return nil, errors.New("at least one application service is required")
	}
	return result, nil
}

func mapsValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
func asMap(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func clone(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func adjustServices(app, infra map[string]any, source string, classification map[string]string) error {
	for name, raw := range app {
		service, ok := asMap(raw)
		if !ok {
			return fmt.Errorf("invalid application service %s", name)
		}
		if _, exists := service["network_mode"]; exists {
			return fmt.Errorf("application %s uses network_mode; it cannot be isolated automatically", name)
		}
		delete(service, "container_name")
		// Published host ports would conflict between Blue and Green.
		if ports, ok := service["ports"].([]any); ok && len(ports) > 0 {
			var exposed []any
			if previous, ok := service["expose"].([]any); ok {
				exposed = append(exposed, previous...)
			}
			for _, entry := range ports {
				switch port := entry.(type) {
				case map[string]any:
					if proto, exists := port["protocol"].(string); exists && proto != "tcp" {
						return fmt.Errorf("application %s publishes a non-TCP port; handle it explicitly", name)
					}
					target, exists := port["target"]
					if !exists {
						return fmt.Errorf("application %s has a port without a target", name)
					}
					exposed = append(exposed, fmt.Sprint(target))
				case string:
					parts := strings.Split(port, ":")
					target := parts[len(parts)-1]
					if strings.Contains(target, "/") && !strings.HasSuffix(target, "/tcp") {
						return fmt.Errorf("application %s publishes a non-TCP port; handle it explicitly", name)
					}
					exposed = append(exposed, strings.TrimSuffix(target, "/tcp"))
				default:
					return fmt.Errorf("unsupported port mapping in %s", name)
				}
			}
			service["expose"] = exposed
		}
		delete(service, "ports")
		service["image"] = "${" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_IMAGE}"
		if err := pruneDependencies(service, classification, "infra", name); err != nil {
			return err
		}
	}
	for name, raw := range infra {
		service, ok := asMap(raw)
		if !ok {
			return fmt.Errorf("invalid infrastructure service %s", name)
		}
		if err := pruneDependencies(service, classification, "app", name); err != nil {
			return err
		}
	}
	return nil
}

func pruneDependencies(service map[string]any, class map[string]string, excluded, serviceName string) error {
	deps := service["depends_on"]
	if deps == nil {
		return nil
	}
	switch list := deps.(type) {
	case map[string]any:
		for name := range list {
			if class[name] == excluded {
				if excluded == "app" {
					return fmt.Errorf("infrastructure service %s depends on application service %s", serviceName, name)
				}
				delete(list, name)
			}
		}
		if len(list) == 0 {
			delete(service, "depends_on")
		}
	case []any:
		kept := []any{}
		for _, item := range list {
			dep, ok := item.(string)
			if !ok {
				return fmt.Errorf("invalid depends_on in %s", serviceName)
			}
			if class[dep] == excluded {
				if excluded == "app" {
					return fmt.Errorf("infrastructure service %s depends on application service %s", serviceName, dep)
				}
				continue
			}
			kept = append(kept, dep)
		}
		if len(kept) == 0 {
			delete(service, "depends_on")
		} else {
			service["depends_on"] = kept
		}
	default:
		return fmt.Errorf("invalid depends_on in %s", serviceName)
	}
	return nil
}

func prepareNetworks(app, infra map[string]any, appServices, infraServices map[string]any) error {
	original, _ := asMap(app["networks"])
	if _, exists := original["bluegreen"]; exists {
		return errors.New("source network name bluegreen is reserved")
	}
	// Keep existing per-slot networks on the application, and share networks used by infrastructure.
	appNets := map[string]any{}
	infraNets := map[string]any{}
	for name, raw := range original {
		appUsed := networkUsed(appServices, name)
		infraUsed := networkUsed(infraServices, name)
		if !appUsed && !infraUsed {
			continue
		}
		if infraUsed {
			infraNets[name] = raw
		}
		if appUsed {
			if infraUsed {
				value, _ := asMap(raw)
				if value != nil && value["external"] == true {
					appNets[name] = raw
				} else {
					physical := "${PROJECT_NAME}-infrastructure_" + name
					if value != nil {
						if custom, ok := value["name"].(string); ok && custom != "" {
							physical = custom
						}
					}
					appNets[name] = map[string]any{"external": true, "name": physical}
				}
			} else {
				appNets[name] = raw
			}
		}
	}
	appNets["bluegreen"] = map[string]any{"external": true, "name": "${DEPLOY_NETWORK}"}
	infraNets["bluegreen"] = map[string]any{"name": "${DEPLOY_NETWORK}"}
	app["networks"] = appNets
	infra["networks"] = infraNets
	for _, group := range []struct {
		services map[string]any
		app bool
	}{{appServices,true},{infraServices,false}} {
		for name, raw := range group.services {
			service, _ := asMap(raw)
			if _, has := service["network_mode"]; has {
				continue
			}
			net, err := asNetworkMap(service["networks"])
			if err != nil {
				return err
			}
			if group.app {
				net["bluegreen"] = map[string]any{
					"aliases": []any{"${PROJECT_NAME}-${DEPLOYMENT_SLOT}-" + name},
				}
			} else {
				net["bluegreen"] = nil
			}
			service["networks"] = net
		}
	}
	return nil
}

func asNetworkMap(raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	if m, ok := asMap(raw); ok {
		return m, nil
	}
	if a, ok := raw.([]any); ok {
		m := map[string]any{}
		for _, e := range a {
			s, ok := e.(string)
			if !ok {
				return nil, errors.New("unsupported network definition")
			}
			m[s] = nil
		}
		return m, nil
	}
	return nil, errors.New("unsupported network definition")
}
func networkUsed(services map[string]any, name string) bool {
	for _, raw := range services {
		s, _ := asMap(raw)
		switch n := s["networks"].(type) {
		case map[string]any:
			if _, ok := n[name]; ok {
				return true
			}
		case []any:
			for _, e := range n {
				if e == name {
					return true
				}
			}
		}
	}
	return false
}

func prepareVolumes(app, infra map[string]any, appServices, infraServices map[string]any) error {
	original, _ := asMap(app["volumes"])
	appDefs := map[string]any{}
	infraDefs := map[string]any{}
	for name, def := range original {
		a := volumeUsed(appServices, name)
		i := volumeUsed(infraServices, name)
		if a && i {
			return fmt.Errorf("named volume %q is shared by application and infrastructure; split ownership explicitly before running init", name)
		}
		if a {
			appDefs[name] = def
		}
		if i {
			infraDefs[name] = def
		}
	}
	if len(appDefs) > 0 {
		app["volumes"] = appDefs
	} else {
		delete(app, "volumes")
	}
	if len(infraDefs) > 0 {
		infra["volumes"] = infraDefs
	} else {
		delete(infra, "volumes")
	}
	return nil
}
func volumeUsed(services map[string]any, name string) bool {
	for _, raw := range services {
		s, _ := asMap(raw)
		list, _ := s["volumes"].([]any)
		for _, x := range list {
			switch v := x.(type) {
			case string:
				if strings.SplitN(v, ":", 2)[0] == name {
					return true
				}
			case map[string]any:
				if v["source"] == name {
					return true
				}
			}
		}
	}
	return false
}

func checkSharedVolumes(source map[string]any, services map[string]any, class map[string]string) error {
	defs, _ := asMap(source["volumes"])
	for name := range defs {
		a := false
		i := false
		for serviceName, raw := range services {
			single := map[string]any{serviceName: raw}
			if volumeUsed(single, name) {
				if class[serviceName] == "app" {
					a = true
				} else {
					i = true
				}
			}
		}
		if a && i {
			return fmt.Errorf("volume %s is shared between slots and infrastructure; separate it explicitly", name)
		}
	}
	return nil
}

func rebasePaths(doc map[string]any, source, destination string) {
	if source == "" {
		return
	}
	prefix, err := filepath.Rel(destination, filepath.Dir(source))
	if err != nil {
		return
	}
	rewrite := func(p string) string {
		if p == "." || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
			return filepath.ToSlash(filepath.Clean(filepath.Join(prefix, p)))
		}
		return p
	}
	services, _ := asMap(doc["services"])
	for _, raw := range services {
		s, _ := asMap(raw)
		if build, ok := asMap(s["build"]); ok {
			if v, yes := build["context"].(string); yes {
				build["context"] = rewrite(v)
			}
		} else if v, ok := s["build"].(string); ok {
			s["build"] = rewrite(v)
		}
		if v, ok := s["env_file"].(string); ok {
			s["env_file"] = rewrite(v)
		}
		if list, ok := s["env_file"].([]any); ok {
			for i, v := range list {
				if str, yes := v.(string); yes {
					list[i] = rewrite(str)
				} else if m, yes := asMap(v); yes {
					if str, yes := m["path"].(string); yes {
						m["path"] = rewrite(str)
					}
				}
			}
		}
		if list, ok := s["volumes"].([]any); ok {
			for i, v := range list {
				if str, yes := v.(string); yes {
					parts := strings.SplitN(str, ":", 2)
					if len(parts) == 2 {
						parts[0] = rewrite(parts[0])
						list[i] = strings.Join(parts, ":")
					}
				} else if m, yes := asMap(v); yes && m["type"] == "bind" {
					if str, yes := m["source"].(string); yes {
						m["source"] = rewrite(str)
					}
				}
			}
		}
	}
	for _, key := range []string{"configs", "secrets"} {
		if group, ok := asMap(doc[key]); ok {
			for _, raw := range group {
				if m, ok := asMap(raw); ok {
					if file, ok := m["file"].(string); ok {
						m["file"] = rewrite(file)
					}
				}
			}
		}
	}
}

// Write never overwrites an existing file with different contents.
func Write(root string, files []File) error {
	for i := range files {
		if files[i].Path != ".env.example" {
			continue
		}
		target := filepath.Join(root, files[i].Path)
		existing, err := os.ReadFile(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			present := map[string]bool{}
			for _, line := range strings.Split(string(existing), "\n") {
				if idx := strings.IndexByte(line, '='); idx > 0 {
					present[strings.TrimSpace(line[:idx])] = true
				}
			}
			var missing []string
			for _, line := range strings.Split(strings.TrimSpace(string(files[i].Data)), "\n") {
				key := strings.TrimSuffix(line, "=")
				if key != "" && !present[key] {
					missing = append(missing, line)
				}
			}
			merged := string(existing)
			if len(missing) > 0 {
				if len(merged) > 0 && !strings.HasSuffix(merged, "\n") {
					merged += "\n"
				}
				merged += strings.Join(missing, "\n") + "\n"
			}
			files[i].Data = []byte(merged)
		}
	}
	for _, file := range files {
		target := filepath.Join(root, file.Path)
		if err := within(root, target); err != nil {
			return err
		}
		current, err := os.ReadFile(target)
		if err == nil && !bytes.Equal(current, file.Data) && file.Path != ".env.example" {
			return fmt.Errorf("%s already exists with different contents; no files were changed", file.Path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, file := range files {
		target := filepath.Join(root, file.Path)
		if current, err := os.ReadFile(target); err == nil {
			if bytes.Equal(current, file.Data) {
				continue
			}
			if file.Path == ".env.example" {
				if err := os.WriteFile(target, file.Data, 0644); err != nil {
					return err
				}
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.Data, 0644); err != nil {
			return err
		}
	}
	return nil
}
func within(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path escapes project: %s", target)
	}
	return nil
}

func marshalYAML(model map[string]any) []byte {
	var b strings.Builder
	yamlValue(&b, model, 0)
	return []byte(b.String())
}
func yamlValue(b *strings.Builder, v any, indent int) {
	p := strings.Repeat(" ", indent)
	switch x := v.(type) {
	case map[string]any:
		ks := keys(x)
		sort.SliceStable(ks, func(i, j int) bool { return order(ks[i]) < order(ks[j]) })
		for _, k := range ks {
			item := x[k]
			b.WriteString(p + quoteKey(k) + ":")
			if isCollection(item) {
				b.WriteString("\n")
				yamlValue(b, item, indent+2)
			} else {
				b.WriteString(" " + scalar(item) + "\n")
			}
		}
	case []any:
		for _, item := range x {
			b.WriteString(p + "-")
			if isCollection(item) {
				b.WriteString("\n")
				yamlValue(b, item, indent+2)
			} else {
				b.WriteString(" " + scalar(item) + "\n")
			}
		}
	}
}
func order(key string) int {
	switch key {
	case "name":
		return 0
	case "services":
		return 1
	case "networks":
		return 2
	case "volumes":
		return 3
	default:
		return 4
	}
}
func isCollection(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	}
	return false
}
func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "{}"
	case []any:
		return "[]"
	case string:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
func quoteKey(s string) string {
	if namePattern.MatchString(s) {
		return s
	}
	return scalar(s)
}
