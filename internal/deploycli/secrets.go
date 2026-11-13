package deploycli

import (
    "bufio"
    "context"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func checkSecrets(args []string, out io.Writer) error {
    if len(args) == 0 {
        return errors.New("usage: bluegreen secrets check|add|list|fields|setup|reconcile")
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
    case "reconcile":
        flags := flag.NewFlagSet("secrets reconcile", flag.ContinueOnError)
        flags.SetOutput(io.Discard)
        root := flags.String("dir", ".", "project deployment directory")
        project := flags.String("project", "", "deployment project")
        if err := flags.Parse(args[1:]); err != nil { return err }
        if flags.NArg() != 0 || *project == "" {
            return errors.New("usage: bluegreen secrets reconcile --project NAME [--dir PATH]")
        }
        controller, err := newController(*root, "", 0)
        if err != nil { return err }
        if err := controller.ReconcileSecrets(context.Background(), *project); err != nil { return err }
        _, err = fmt.Fprintf(out, "OpenBao reconciliation completed for %s\n", *project)
        return err
    case "add":
        return addSecretReference(args[1:], out)
    case "list", "fields":
        return discoverSecrets(args[0], args[1:], out)
    case "setup":
        return setupSecrets(args[1:], os.Stdin, out)
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
    if *path=="" && *field=="" {
        if err:=selectKVReference(*project,*mount,target,path,field,os.Stdin,out);err!=nil{return err}
    } else if *path=="" || *field=="" {
        return errors.New("provide both --path and --field or omit both for guided selection")
    }
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

func discoverSecrets(action string, args []string, out io.Writer) error {
    flags := flag.NewFlagSet("secrets "+action, flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    root := flags.String("dir", ".", "deployment directory")
    project := flags.String("project", "", "deployment project")
    mount := flags.String("mount", "", "KV v2 mount (required before the first association)")
    path := flags.String("path", "", "KV secret path (fields only)")
    if err := flags.Parse(args); err != nil { return err }
    if flags.NArg()!=0 || *project=="" || (action=="fields" && *path=="") || (action=="list" && *path!="") {
        return errors.New("usage: bluegreen secrets "+action+" --project NAME [--mount KV] [--path SECRET (fields only)]")
    }
    configPath := filepath.Join(*root, "deploy", "secrets.json")
    data,err := os.ReadFile(configPath)
    if err==nil {
        configured,err:=openbao.Parse(data,*project)
        if err!=nil{return err}
        if *mount!="" && *mount!=configured.Mount {return errors.New("KV mount differs from existing project configuration")}
        *mount=configured.Mount
    }else if !errors.Is(err,os.ErrNotExist){return err}
    if *mount=="" {return errors.New("specify --mount for the first discovery (no project KV configuration exists)")}
    client,err:=openbao.NewClient(*project)
    if err!=nil{return err}
    var names []string
    if action=="list" {names,err=client.ListKeys(context.Background(),*project,*mount)}else{
        names,err=client.ListFields(context.Background(),*project,*mount,*path)
    }
    if err!=nil{return err}
    for _,name:=range names {
        if _,err:=fmt.Fprintln(out,name);err!=nil{return err}
    }
    return nil
}

// setupSecrets is a one-time guided server operation. AppRole credentials
// already provisioned by an administrator are read from protected files.
func setupSecrets(args []string, input io.Reader, out io.Writer) error {
    flags:=flag.NewFlagSet("secrets setup",flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    project:=flags.String("project","","deployment project")
    if err:=flags.Parse(args);err!=nil{return err}
    if flags.NArg()!=0 || *project=="" {return errors.New("usage: sudo bluegreen secrets setup --project NAME")}
    provision,err:=promptProxyProvision(*project,input,out)
    if err!=nil{return err}
    if err:=openbao.ProvisionProxy(provision);err!=nil{return err}
    _,err=fmt.Fprintf(out,"OpenBao Proxy and secret reconciler configured for %s\n",*project)
    return err
}

func promptProxyProvision(project string, input io.Reader, out io.Writer) (openbao.ProxyProvision,error) {
    reader:=bufio.NewReader(input)
    ask:=func(prompt string)(string,error){
        if _,err:=fmt.Fprint(out,prompt);err!=nil{return "",err}
        value,err:=reader.ReadString('\n')
        if err!=nil && !(errors.Is(err,io.EOF) && value!=""){return "",fmt.Errorf("missing setup input: %s",prompt)}
        value=strings.TrimSpace(value)
        if value=="" {return "",fmt.Errorf("missing setup input: %s",prompt)}
        return value,nil
    }
    var p openbao.ProxyProvision
    p.Project=project
    questions:=[]struct{prompt string; destination *string}{
        {"OpenBao HTTPS URL: ",&p.ServerURL},
        {"Linux deployment user: ",&p.DeployUser},
        {"Existing AppRole role ID file: ",&p.RoleIDFile},
        {"Existing AppRole secret ID file: ",&p.SecretIDFile},
    }
    for _,q:=range questions{
        value,err:=ask(q.prompt);if err!=nil{return openbao.ProxyProvision{},err}
        *q.destination=value
    }
    if err:=openbao.ValidateProvision(p);err!=nil{return openbao.ProxyProvision{},err}
    return p,nil
}

func selectKVReference(project, mount, manifestPath string, path, field *string, input io.Reader, output io.Writer) error {
    if data,err:=os.ReadFile(manifestPath);err==nil {
        manifest,err:=openbao.Parse(data,project)
        if err!=nil{return err}
        if mount!="" && mount!=manifest.Mount{return errors.New("OpenBao mount differs from existing project configuration")}
        mount=manifest.Mount
    }else if !errors.Is(err,os.ErrNotExist){return err}
    if mount==""{return errors.New("specify --mount to discover a project's first KV references")}
    client,err:=openbao.NewClient(project)
    if err!=nil{return err}
    keys,err:=client.ListKeys(context.Background(),project,mount)
    if err!=nil{return err}
    reader:=bufio.NewReader(input)
    *path,err=chooseKVName(reader,output,"KV secret",keys)
    if err!=nil{return err}
    fields,err:=client.ListFields(context.Background(),project,mount,*path)
    if err!=nil{return err}
    *field,err=chooseKVName(reader,output,"KV field",fields)
    return err
}

func chooseKVName(input *bufio.Reader, output io.Writer, label string, choices []string) (string,error) {
    if len(choices)==0{return "",fmt.Errorf("no %s available",label)}
    for i,choice:=range choices {
        if _,err:=fmt.Fprintf(output,"%d. %s\n",i+1,choice);err!=nil{return "",err}
    }
    if _,err:=fmt.Fprintf(output,"Select %s (1-%d): ",label,len(choices));err!=nil{return "",err}
    selected,err:=input.ReadString('\n')
    if err!=nil && !(errors.Is(err,io.EOF)&&selected!=""){return "",fmt.Errorf("missing selection for %s",label)}
    selected=strings.TrimSpace(selected)
    index,err:=strconv.Atoi(selected)
    if err!=nil || index<1 || index>len(choices) {return "",fmt.Errorf("invalid %s selection",label)}
    return choices[index-1],nil
}
