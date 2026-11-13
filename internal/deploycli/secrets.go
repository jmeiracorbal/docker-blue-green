package deploycli

import (
    "bufio"
    "context"
    "errors"
    "flag"
    "fmt"
    "io"
    "os"
    "strconv"
    "strings"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func checkSecrets(args []string, out io.Writer) error {
    if len(args)==0 { return errors.New("usage: bluegreen secrets check|add|list|fields|setup|reconcile") }
    switch args[0] {
    case "check":
        flags:=flag.NewFlagSet("secrets check",flag.ContinueOnError)
        flags.SetOutput(io.Discard)
        dir:=flags.String("dir",".","project directory")
        project:=flags.String("project","","optional project confirmation")
        if err:=flags.Parse(args[1:]);err!=nil{return err}
        if flags.NArg()!=0{return errors.New("usage: bluegreen secrets check [--project NAME] [--dir PATH]")}
        cfg,err:=config.Load(*dir);if err!=nil{return err}
        if err:=matchProject(cfg,*project);err!=nil{return err}
        manifest,ok:=cfg.Secrets()
        if !ok{return errors.New("no OpenBao references configured")}
        client,err:=openbao.NewClient(cfg.Project)
        if err!=nil{return err}
        if err:=client.Check(context.Background(),cfg.Project,manifest);err!=nil{return err}
        _,err=fmt.Fprintf(out,"%d OpenBao secret references verified for %s\n",len(manifest.Secrets),cfg.Project)
        return err
    case "reconcile":
        flags:=flag.NewFlagSet("secrets reconcile",flag.ContinueOnError)
        flags.SetOutput(io.Discard)
        dir:=flags.String("dir",".","project directory")
        project:=flags.String("project","","optional project confirmation")
        if err:=flags.Parse(args[1:]);err!=nil{return err}
        if flags.NArg()!=0{return errors.New("usage: bluegreen secrets reconcile [--project NAME] [--dir PATH]")}
        cfg,err:=config.Load(*dir);if err!=nil{return err}
        if err:=matchProject(cfg,*project);err!=nil{return err}
        controller,err:=newController(*dir,"",0);if err!=nil{return err}
        if err:=controller.ReconcileSecrets(context.Background(),cfg.Project);err!=nil{return err}
        _,err=fmt.Fprintf(out,"OpenBao reconciliation completed for %s\n",cfg.Project)
        return err
    case "add":
        return addSecretReference(args[1:],out)
    case "list","fields":
        return discoverSecrets(args[0],args[1:],out)
    case "setup":
        return setupSecrets(args[1:],os.Stdin,out)
    default:
        return fmt.Errorf("unknown secrets command %q",args[0])
    }
}

func matchProject(cfg config.Document, provided string) error {
    if cfg.Project==""{return errors.New("run bluegreen configure to set the project identifier")}
    if provided!=""&&provided!=cfg.Project{return errors.New("project differs from deploy/bluegreen.yml")}
    return nil
}

func configuredMount(cfg config.Document, requested string) (string,error) {
    if cfg.OpenBao==nil {
        if requested==""{return "",errors.New("specify --mount for the first OpenBao reference")}
        return requested,nil
    }
    if requested!=""&&requested!=cfg.OpenBao.Mount{
        return "",errors.New("OpenBao mount differs from deploy/bluegreen.yml")
    }
    return cfg.OpenBao.Mount,nil
}

func addSecretReference(args []string,out io.Writer) error {
    flags:=flag.NewFlagSet("secrets add",flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    dir:=flags.String("dir",".","project directory")
    project:=flags.String("project","","optional project confirmation")
    mount:=flags.String("mount","","KV v2 mount")
    service:=flags.String("service","","application service")
    name:=flags.String("name","","Compose secret name")
    path:=flags.String("path","","KV path relative to project prefix")
    field:=flags.String("field","","KV field")
    if err:=flags.Parse(args);err!=nil{return err}
    if flags.NArg()!=0{return errors.New("usage: bluegreen secrets add --service SERVICE --name NAME [--mount KV] [--path PATH --field FIELD] [--dir PATH]")}
    cfg,err:=config.Load(*dir);if err!=nil{return err}
    if err:=matchProject(cfg,*project);err!=nil{return err}
    effectiveMount,err:=configuredMount(cfg,*mount)
    if err!=nil{return err}
    if (*path=="")!=(*field==""){return errors.New("provide both --path and --field or omit both to choose interactively")}
    if *path==""{
        if err:=selectKVReference(cfg.Project,effectiveMount,path,field,os.Stdin,out);err!=nil{return err}
    }
    ref:=openbao.Reference{Service:*service,Name:*name,Path:*path,Field:*field}
    if cfg.OpenBao==nil{cfg.OpenBao=&config.OpenBao{Mount:effectiveMount}}
    for _,prev:=range cfg.OpenBao.Secrets{
        if prev.Service==ref.Service && prev.Name==ref.Name {
            if prev==ref {
                _,err:=fmt.Fprintf(out,"OpenBao reference already configured: %s/%s\n",ref.Service,ref.Name)
                return err
            }
            return errors.New("OpenBao reference already exists with different settings")
        }
    }
    cfg.OpenBao.Secrets=append(cfg.OpenBao.Secrets,ref)
    if err:=cfg.Validate(false);err!=nil{return err}
    if err:=cfg.ValidateApplicationServices(*dir);err!=nil{return err}
    if _,err:=os.Stat(config.Path(*dir));err!=nil{return err}
    if err:=config.Save(*dir,cfg);err!=nil{return err}
    _,err=fmt.Fprintf(out,"OpenBao secret reference configured: %s/%s\n",ref.Service,ref.Name)
    return err
}

func discoverSecrets(action string,args []string,out io.Writer) error {
    flags:=flag.NewFlagSet("secrets "+action,flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    dir:=flags.String("dir",".","project directory")
    project:=flags.String("project","","optional project confirmation")
    mount:=flags.String("mount","","KV v2 mount")
    path:=flags.String("path","","KV secret path (fields only)")
    if err:=flags.Parse(args);err!=nil{return err}
    if flags.NArg()!=0||(action=="fields"&&*path=="")||(action=="list"&&*path!=""){
        return errors.New("usage: bluegreen secrets "+action+" [--project NAME] [--mount KV] [--path SECRET (fields only)] [--dir PATH]")
    }
    cfg,err:=config.Load(*dir);if err!=nil{return err}
    if err:=matchProject(cfg,*project);err!=nil{return err}
    effectiveMount,err:=configuredMount(cfg,*mount);if err!=nil{return err}
    client,err:=openbao.NewClient(cfg.Project)
    if err!=nil{return err}
    var names []string
    if action=="list"{
        names,err=client.ListKeys(context.Background(),cfg.Project,effectiveMount)
    }else{
        names,err=client.ListFields(context.Background(),cfg.Project,effectiveMount,*path)
    }
    if err!=nil{return err}
    for _,name:=range names{
        if _,err:=fmt.Fprintln(out,name);err!=nil{return err}
    }
    return nil
}

// setupSecrets remains a host administrator operation. No AppRole credential
// values or proxy socket paths are accepted as CLI flags.
func setupSecrets(args []string,input io.Reader,out io.Writer) error {
    flags:=flag.NewFlagSet("secrets setup",flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    project:=flags.String("project","","project identifier")
    if err:=flags.Parse(args);err!=nil{return err}
    if flags.NArg()!=0||*project==""{return errors.New("usage: sudo bluegreen secrets setup --project NAME")}
    provision,err:=promptProxyProvision(*project,input,out)
    if err!=nil{return err}
    if err:=openbao.ProvisionProxy(provision);err!=nil{return err}
    _,err=fmt.Fprintf(out,"OpenBao Proxy and secret reconciler configured for %s\n",*project)
    return err
}

func promptProxyProvision(project string,input io.Reader,out io.Writer)(openbao.ProxyProvision,error){
    reader:=bufio.NewReader(input)
    ask:=func(prompt string)(string,error){
        if _,err:=fmt.Fprint(out,prompt);err!=nil{return "",err}
        value,err:=reader.ReadString('\n')
        if err!=nil && !(errors.Is(err,io.EOF)&&value!=""){return "",fmt.Errorf("missing setup input: %s",prompt)}
        value=strings.TrimSpace(value)
        if value==""{return "",fmt.Errorf("missing setup input: %s",prompt)}
        return value,nil
    }
    var p openbao.ProxyProvision
    p.Project=project
    prompts:=[]struct{label string; target *string}{
        {"OpenBao HTTPS URL: ",&p.ServerURL},
        {"Linux deployment user: ",&p.DeployUser},
        {"Existing AppRole role ID file: ",&p.RoleIDFile},
        {"Existing AppRole secret ID file: ",&p.SecretIDFile},
    }
    for _,q:=range prompts{
        val,err:=ask(q.label);if err!=nil{return openbao.ProxyProvision{},err}
        *q.target=val
    }
    if err:=openbao.ValidateProvision(p);err!=nil{return openbao.ProxyProvision{},err}
    return p,nil
}

func selectKVReference(project,mount string,path,field *string,input io.Reader,out io.Writer) error {
    client,err:=openbao.NewClient(project);if err!=nil{return err}
    keys,err:=client.ListKeys(context.Background(),project,mount);if err!=nil{return err}
    reader:=bufio.NewReader(input)
    *path,err=chooseKVName(reader,out,"KV secret",keys)
    if err!=nil{return err}
    fields,err:=client.ListFields(context.Background(),project,mount,*path)
    if err!=nil{return err}
    *field,err=chooseKVName(reader,out,"KV field",fields)
    return err
}

func chooseKVName(input *bufio.Reader,output io.Writer,label string,choices []string)(string,error){
    if len(choices)==0{return "",fmt.Errorf("no %s available",label)}
    for i,choice:=range choices{
        if _,err:=fmt.Fprintf(output,"%d. %s\n",i+1,choice);err!=nil{return "",err}
    }
    if _,err:=fmt.Fprintf(output,"Select %s (1-%d): ",label,len(choices));err!=nil{return "",err}
    selected,err:=input.ReadString('\n')
    if err!=nil && !(errors.Is(err,io.EOF)&&selected!=""){return "",fmt.Errorf("missing selection for %s",label)}
    index,err:=strconv.Atoi(strings.TrimSpace(selected))
    if err!=nil||index<1||index>len(choices){return "",fmt.Errorf("invalid %s selection",label)}
    return choices[index-1],nil
}
