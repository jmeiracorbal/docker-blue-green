package deploycli

import (
    "bufio"
    "errors"
    "flag"
    "fmt"
    "io"
    "strconv"
    "strings"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
)

// Configure is the same interactive entry point for every Compose project.
// It edits project identity and routes in deploy/bluegreen.yml only.
func Configure(args []string, input io.Reader, output io.Writer) error {
    flags:=flag.NewFlagSet("configure",flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    dir:=flags.String("dir",".","project directory")
    project:=flags.String("project","","project identity")
    host:=flags.String("host","","public route hostname")
    service:=flags.String("service","","application service")
    port:=flags.Int("port",0,"container TCP port")
    if err:=flags.Parse(args);err!=nil{return err}
    if flags.NArg()!=0{return errors.New("usage: bluegreen configure [--dir PATH] [--project NAME] [--host HOST --service SERVICE --port PORT]")}

    cfg,err:=config.Load(*dir)
    if err!=nil{return err}
    reader:=bufio.NewReader(input)
    ask:=func(question string)(string,error){
        if _,err:=fmt.Fprint(output,question);err!=nil{return "",err}
        value,err:=reader.ReadString('\n')
        if err!=nil && !(errors.Is(err,io.EOF)&&value!=""){return "",errors.New("missing interactive configuration input")}
        value=strings.TrimSpace(value)
        if value==""{return "",errors.New("empty interactive configuration value")}
        return value,nil
    }

    if cfg.Project!="" {
        if *project!="" && cfg.Project!=*project {return errors.New("project identity cannot be replaced; initialize a separate project")}
    } else {
        if *project=="" { *project,err=ask("Project identifier: ");if err!=nil{return err} }
        cfg.Project=*project
    }

    provided:=*host!="" || *service!="" || *port!=0
    if provided && (*host=="" || *service=="" || *port==0){
        return errors.New("route configuration requires --host, --service and --port together")
    }
    if !provided && len(cfg.Routes)==0 {
        *host,err=ask("Public hostname: ");if err!=nil{return err}
        *service,err=ask("Application service: ");if err!=nil{return err}
        value,err:=ask("Container TCP port: ");if err!=nil{return err}
        *port,err=strconv.Atoi(value)
        if err!=nil{return errors.New("container TCP port must be a number")}
        provided=true
    }
    if provided {
        found:=false
        for _,route:=range cfg.Routes {
            if strings.EqualFold(route.Host,*host){
                if route.Service!=*service || route.Port!=*port{
                    return fmt.Errorf("route %q already exists with different settings",*host)
                }
                found=true
            }
        }
        if !found {
            cfg.Routes=append(cfg.Routes,config.Route{Host:*host,Service:*service,Port:*port})
        }
    }
    if err:=cfg.Validate(true);err!=nil{return err}
    if err:=config.Save(*dir,cfg);err!=nil{return err}
    _,err=fmt.Fprintf(output,"Configured %s in %s\n",cfg.Project,config.Path(*dir))
    return err
}

func Config(args []string, out io.Writer) error {
    if len(args)==0{return errors.New("usage: bluegreen config check|export [--dir PATH]")}
    action:=args[0]
    if action!="check"&&action!="export"{return errors.New("usage: bluegreen config check|export [--dir PATH]")}
    flags:=flag.NewFlagSet("config "+action,flag.ContinueOnError)
    flags.SetOutput(io.Discard)
    dir:=flags.String("dir",".","project directory")
    if err:=flags.Parse(args[1:]);err!=nil{return err}
    if flags.NArg()!=0{return errors.New("unexpected config arguments")}
    cfg,err:=config.Load(*dir)
    if err!=nil{return err}
    if err:=cfg.Validate(true);err!=nil{return err}
    if action=="export" {
        b,err:=cfg.Export()
        if err!=nil{return err}
        _,err=fmt.Fprintln(out,string(b))
        return err
    }
    // Use JSON only for the machine-readable export. All other commands use
    // exactly the same YAML document and never expose secret values.
    _,err=fmt.Fprintf(out,"Validated project %s (%d routes)\n",cfg.Project,len(cfg.Routes))
    return err
}

