package deploycli

import (
    "context"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func checkSecrets(args []string, out io.Writer) error {
    if len(args) == 0 {
        return errors.New("usage: bluegreen secrets check|add|configure")
    }
    switch args[0] {
    case "check":
        flags := flag.NewFlagSet("secrets check", flag.ContinueOnError)
        flags.SetOutput(io.Discard)
        root := flags.String("dir", ".", "project deployment directory")
        project := flags.String("project", "", "deployment project name")
        if err := flags.Parse(args[1:]); err != nil { return err }
        if flags.NArg() != 0 || *project == "" {
            return errors.New("usage: bluegreen secrets check --project NAME [--dir PATH]")
        }
        manifest, err := openbao.Load(filepath.Join(*root, "deploy", "secrets.json"), *project)
        if err != nil { return err }
        client, err := openbao.NewClient(*project)
        if err != nil { return err }
        if err := client.Check(context.Background(), *project, manifest); err != nil { return err }
        _, err = fmt.Fprintf(out, "%d OpenBao secret references verified for %s\n", len(manifest.Secrets), *project)
        return err
    case "add":
        return addSecretReference(args[1:], out)
    case "configure":
        flags := flag.NewFlagSet("secrets configure", flag.ContinueOnError)
        flags.SetOutput(io.Discard)
        project := flags.String("project", "", "deployment project name")
        server := flags.String("server", "", "HTTPS origin of the OpenBao server")
        account := flags.String("user", "", "existing Linux deployment account")
        roleFile := flags.String("role-id-file", "", "existing AppRole role identifier file")
        secretFile := flags.String("secret-id-file", "", "existing AppRole secret identifier file")
        if err := flags.Parse(args[1:]); err != nil { return err }
        if flags.NArg() != 0 {
            return errors.New("usage: bluegreen secrets configure --project NAME --server HTTPS_URL --user ACCOUNT --role-id-file PATH --secret-id-file PATH")
        }
        provision := openbao.ProxyProvision{
            Project: *project, ServerURL: *server, DeployUser: *account,
            RoleIDFile: *roleFile, SecretIDFile: *secretFile,
        }
        if err := openbao.ProvisionProxy(provision); err != nil { return err }
        _, err := fmt.Fprintf(out, "OpenBao Proxy configured for %s\n", provision.Project)
        return err
    default:
        return fmt.Errorf("unknown secrets command %q", args[0])
    }
}

func addSecretReference(args []string, out io.Writer) error {
    flags := flag.NewFlagSet("secrets add", flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    root := flags.String("dir", ".", "project deployment directory")
    project := flags.String("project", "", "deployment project name")
    mount := flags.String("mount", "", "OpenBao KV v2 mount")
    service := flags.String("service", "", "application service")
    name := flags.String("name", "", "Docker Compose secret name")
    path := flags.String("path", "", "path relative to projects/PROJECT")
    field := flags.String("field", "", "field in the KV v2 record")
    if err := flags.Parse(args); err != nil { return err }
    if flags.NArg() != 0 {
        return errors.New("usage: bluegreen secrets add --project NAME --mount MOUNT --service SERVICE --name NAME --path PATH --field FIELD [--dir PATH]")
    }
    target := filepath.Join(*root, "deploy", "secrets.json")
    ref := openbao.Reference{Service: *service, Name: *name, Path: *path, Field: *field}
    manifest := openbao.Manifest{Mount: *mount, Secrets: []openbao.Reference{ref}}
    data, err := os.ReadFile(target)
    if err == nil {
        existing, err := openbao.Parse(data, *project)
        if err != nil { return err }
        if *mount != "" && existing.Mount != *mount { return errors.New("OpenBao mount differs from existing configuration") }
        manifest.Mount = existing.Mount
        manifest.Secrets = append(existing.Secrets, ref)
        for _, prev := range existing.Secrets {
            if prev.Service == ref.Service && prev.Name == ref.Name {
                if prev == ref {
                    _, err := fmt.Fprintf(out, "OpenBao secret reference already configured: %s/%s\n", ref.Service, ref.Name)
                    return err
                }
                return errors.New("OpenBao secret reference already exists with different settings")
            }
        }
    } else if !errors.Is(err, os.ErrNotExist) { return err }
    if err := manifest.Validate(*project); err != nil { return err }
    if _, err := os.Stat(filepath.Join(*root, "deploy", "compose.yml")); err != nil {
        return fmt.Errorf("initialize the project before configuring secrets: %w", err)
    }
    for _, e := range strings.Split(*path, "/") { if e == "" { return errors.New("invalid OpenBao KV path") } }
    encoded, err := json.MarshalIndent(manifest, "", "  ")
    if err != nil { return err }
    encoded = append(encoded, '\n')
    if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil { return err }
    tmp, err := os.CreateTemp(filepath.Dir(target), ".secrets-")
    if err != nil { return err }
    defer os.Remove(tmp.Name())
    if err := tmp.Chmod(0600); err != nil { tmp.Close(); return err }
    if _, err := tmp.Write(encoded); err != nil { tmp.Close(); return err }
    if err := tmp.Close(); err != nil { return err }
    if err := os.Rename(tmp.Name(), target); err != nil { return err }
    _, err = fmt.Fprintf(out, "OpenBao secret reference configured: %s/%s\n", ref.Service, ref.Name)
    return err
}
