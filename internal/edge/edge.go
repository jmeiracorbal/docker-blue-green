package edge

import (
    "bytes"
    "context"
    "encoding/json"
    _ "embed"
    "errors"
    "fmt"
    "net"
    "os"
    "path/filepath"
    "regexp"
    "sort"
    "strconv"
    "strings"
    "sync"
    "syscall"
)

//go:embed templates/compose.yml
var compose string

//go:embed templates/Caddyfile
var caddyfile string

var projectName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
var siteHeader = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9.-]*) {$`)
var imageName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./:@-]+$`)
var emailName = regexp.MustCompile(`^[^[:space:]@]+@[^[:space:]@]+$`)
var networkName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

type Runner interface {
    Run(context.Context, string, []string, string, ...string) error
}
type Route struct {
    Host string
    Service string
    Port int
    Shared bool
}
type Manager struct {
    Root string
    ProjectEnvFile string
    Runner Runner
}
func Root(projectRoot string) string {
    return filepath.Join(filepath.Dir(projectRoot), "edge")
}
var edgeProcessLocks sync.Map

func (m Manager) lock() (func(),error) {
    root,err:=filepath.Abs(m.Root)
    if err!=nil{return nil,err}
    instance,_:=edgeProcessLocks.LoadOrStore(root,&sync.Mutex{})
    mutex:=instance.(*sync.Mutex)
    mutex.Lock()
    active:=false
    defer func(){if !active{mutex.Unlock()}}()
    if err:=os.MkdirAll(root,0700);err!=nil{return nil,err}
    if err:=os.Chmod(root,0700);err!=nil{return nil,err}
    f,err:=os.OpenFile(filepath.Join(root,"lock"),os.O_CREATE|os.O_RDWR,0600)
    if err!=nil{return nil,err}
    if err=syscall.Flock(int(f.Fd()),syscall.LOCK_EX);err!=nil{f.Close();return nil,err}
    active=true
    return func(){
        _=syscall.Flock(int(f.Fd()),syscall.LOCK_UN)
        _=f.Close()
        mutex.Unlock()
    },nil
}
func (m Manager) env() (map[string]string,error) {
    wanted:=[]string{"DEPLOY_NETWORK","CADDY_IMAGE","CADDY_ACME_EMAIL","HTTP_PORT","HTTPS_PORT"}
    values:=map[string]string{}
    for _,key:=range wanted {values[key]=os.Getenv(key)}
    if m.ProjectEnvFile!="" {
        b,err:=os.ReadFile(m.ProjectEnvFile)
        if err!=nil{return nil,err}
        seen:=map[string]bool{}
        for _,line:=range strings.Split(string(b),"\n") {
            line=strings.TrimSpace(line)
            if line=="" || strings.HasPrefix(line,"#"){continue}
            key,value,ok:=strings.Cut(line,"=")
            if !ok {return nil,errors.New("invalid server .env assignment")}
            key=strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key),"export "))
            if _,allowed:=values[key];!allowed{continue}
            if seen[key]{return nil,fmt.Errorf("duplicate Edge configuration %s",key)}
            seen[key]=true
            value=strings.TrimSpace(value)
            if len(value)>=2 && (value[0]=='"' && value[len(value)-1]=='"' || value[0]=='\u0027' && value[len(value)-1]=='\u0027') {
                value=value[1:len(value)-1]
            }
            values[key]=value
        }
    }
    for _,key:=range wanted {
        if values[key]=="" || strings.ContainsAny(values[key],"\r\n\x00") {
            return nil,fmt.Errorf("Edge requires %s in the project environment",key)
        }
    }
    if !networkName.MatchString(values["DEPLOY_NETWORK"]) || !imageName.MatchString(values["CADDY_IMAGE"]) ||
        !emailName.MatchString(values["CADDY_ACME_EMAIL"]) {
        return nil,errors.New("invalid Edge network, image or ACME email")
    }
    for _,key:=range []string{"HTTP_PORT","HTTPS_PORT"} {
        raw:=values[key]
        if strings.Contains(raw,":"){
            host,number,err:=net.SplitHostPort(raw)
            if err!=nil || net.ParseIP(host)==nil{return nil,fmt.Errorf("invalid Edge %s bind address",key)}
            raw=number
        }
        port,err:=strconv.Atoi(raw)
        if err!=nil || port<1 || port>65535 {return nil,fmt.Errorf("invalid Edge %s",key)}
    }
    if values["HTTP_PORT"]==values["HTTPS_PORT"] {return nil,errors.New("Edge HTTP and HTTPS host ports must differ")}
    return values,nil
}
func encodeEnv(values map[string]string) []byte {
    keys:=make([]string,0,len(values))
    for key:=range values {keys=append(keys,key)}
    sort.Strings(keys)
    var b strings.Builder
    for _,key:=range keys {fmt.Fprintf(&b,"%s=%s\n",key,values[key])}
    return []byte(b.String())
}
func writeAtomic(path string,data []byte,perm os.FileMode) error {
    file,err:=os.CreateTemp(filepath.Dir(path),".edge-")
    if err!=nil{return err}
    defer os.Remove(file.Name())
    if err=file.Chmod(perm);err!=nil{file.Close();return err}
    if _,err=file.Write(data);err!=nil{file.Close();return err}
    if err=file.Sync();err!=nil{file.Close();return err}
    if err=file.Close();err!=nil{return err}
    return os.Rename(file.Name(),path)
}
func ensureManagedFile(path string,data []byte,perm os.FileMode) (bool,error) {
    existing,err:=os.Lstat(path)
    if err==nil {
        if !existing.Mode().IsRegular() {return false,fmt.Errorf("unsafe Edge file %s",path)}
        actual,readErr:=os.ReadFile(path)
        if readErr!=nil{return false,readErr}
        if bytes.Equal(actual,data) {return false,nil}
    } else if !errors.Is(err,os.ErrNotExist) {return false,err}
    return true,writeAtomic(path,data,perm)
}
func (m Manager) composeArgs(args ...string) []string {
    result:=[]string{"compose","--env-file",filepath.Join(m.Root,".env"),"-f",filepath.Join(m.Root,"compose.yml")}
    return append(result,args...)
}
func (m Manager) run(ctx context.Context,args ...string) error {
    return m.Runner.Run(ctx,m.Root,os.Environ(),"docker",m.composeArgs(args...)...)
}
func (m Manager) reload(ctx context.Context) error {
    if err:=m.run(ctx,"exec","-T","caddy","caddy","validate","--config","/etc/caddy/Caddyfile");err!=nil{return fmt.Errorf("validate Edge Caddy: %w",err)}
    if err:=m.run(ctx,"exec","-T","caddy","caddy","reload","--config","/etc/caddy/Caddyfile");err!=nil{return fmt.Errorf("reload Edge Caddy: %w",err)}
    return nil
}
func (m Manager) Ensure(ctx context.Context) error {
    if m.Runner==nil{return errors.New("Edge runner is required")}
    values,err:=m.env()
    if err!=nil{return err}
    unlock,err:=m.lock()
    if err!=nil{return err}
    defer unlock()
    configPath:=filepath.Join(m.Root,".env")
    current,err:=os.ReadFile(configPath)
    newInstall:=errors.Is(err,os.ErrNotExist)
    if errors.Is(err,os.ErrNotExist) {
        entries,readErr:=os.ReadDir(m.Root)
        if readErr!=nil{return readErr}
        for _,entry:=range entries {
            if entry.Name()!="lock" {return errors.New("refusing to adopt an unmanaged Edge installation")}
        }
        if err=writeAtomic(configPath,encodeEnv(values),0600);err!=nil{return err}
    } else if err!=nil{return err
    } else if !bytes.Equal(current,encodeEnv(values)) {
        return errors.New("shared Edge configuration differs from the requested network, image, ACME identity or ports; update Edge separately")
    }
    changed:=false
    for path,data:=range map[string][]byte{
        "compose.yml":[]byte(compose),
        "Caddyfile":[]byte(caddyfile),
    } {
        modified,e:=ensureManagedFile(filepath.Join(m.Root,path),data,0644)
        if e!=nil{return e}
        changed=changed||modified
    }
    if err=os.MkdirAll(filepath.Join(m.Root,"apps"),0750);err!=nil{return err}
    if err=m.run(ctx,"up","-d","--wait");err!=nil{return fmt.Errorf("reconcile shared Edge: %w",err)}
    if changed && !newInstall {
        if err=m.reload(ctx);err!=nil{return err}
    }
    return nil
}
func (m Manager) routeFile(project string) string {
    return filepath.Join(m.Root,"apps",project+".caddy")
}
func (m Manager) RouteFile(project string) string { return m.routeFile(project) }
func render(project,slot string,routes []Route) ([]byte,error) {
    if !projectName.MatchString(project) || (slot!="blue" && slot!="green" && slot!="") {return nil,errors.New("invalid Edge project or slot")}
    cp:=append([]Route(nil),routes...)
    sort.Slice(cp,func(i,j int)bool{return cp[i].Host<cp[j].Host})
    seen:=map[string]bool{}
    var b strings.Builder
    for _,route:=range cp {
        host:=strings.ToLower(route.Host)
        if !hostName.MatchString(host)||strings.Contains(host,"..") ||
            !projectName.MatchString(route.Service)||route.Port<1||route.Port>65535||slot=="" {
            return nil,errors.New("invalid Edge ingress route")
        }
        if seen[host]{return nil,fmt.Errorf("duplicate Edge hostname %s",host)}
        seen[host]=true
        destination:=fmt.Sprintf("%s-%s-%s:%d",project,slot,route.Service,route.Port)
        if route.Shared {destination=fmt.Sprintf("%s-infrastructure-%s:%d",project,route.Service,route.Port)}
        fmt.Fprintf(&b,"%s {\n    encode zstd gzip\n    reverse_proxy %s\n}\n\n",host,destination)
    }
    return []byte(b.String()),nil
}
func hosts(data []byte) ([]string,error) {
    var names []string
    for _,line:=range strings.Split(string(data),"\n") {
        if strings.TrimSpace(line)==""{continue}
        if strings.HasPrefix(line," ")||line=="}"{continue}
        if !siteHeader.MatchString(line) {return nil,errors.New("unmanaged site configuration in Edge apps directory")}
        m:=siteHeader.FindStringSubmatch(line)
        names=append(names,strings.ToLower(m[1]))
    }
    return names,nil
}
func (m Manager) Publish(ctx context.Context,project,slot string,routes []Route) error {
    if m.Runner==nil{return errors.New("Edge runner is required")}
    next,err:=render(project,slot,routes)
    if err!=nil{return err}
    unlock,err:=m.lock()
    if err!=nil{return err}
    defer unlock()
    dir:=filepath.Join(m.Root,"apps")
    entries,err:=os.ReadDir(dir)
    if err!=nil{return err}
    desired:=map[string]bool{}
    for _,route:=range routes {desired[strings.ToLower(route.Host)]=true}
    for _,item:=range entries {
        if item.IsDir() || !strings.HasSuffix(item.Name(),".caddy"){continue}
        if item.Name()==project+".caddy"{continue}
        if !projectName.MatchString(strings.TrimSuffix(item.Name(),".caddy")){return errors.New("unmanaged Edge site filename")}
        info,err:=item.Info()
        if err!=nil{return err}
        if !info.Mode().IsRegular(){return errors.New("unsafe Edge site file")}
        other,err:=os.ReadFile(filepath.Join(dir,item.Name()))
        if err!=nil{return err}
        listed,err:=hosts(other)
        if err!=nil{return err}
        for _,hostname:=range listed {
            if desired[hostname]{return fmt.Errorf("Edge hostname %s is already owned by %s",hostname,item.Name())}
        }
    }
    path:=m.routeFile(project)
    old,err:=os.ReadFile(path)
    existed:=err==nil
    if err!=nil&&!errors.Is(err,os.ErrNotExist){return err}
    if existed {
        if info,e:=os.Lstat(path);e!=nil{return e}else if !info.Mode().IsRegular(){return errors.New("unsafe Edge route file")}
    }
    if existed&&bytes.Equal(old,next){return nil}
    if len(next)==0 {
        if existed {err=os.Remove(path)}
    }else {
        err=writeAtomic(path,next,0644)
    }
    if err!=nil{return err}
    if err=m.reload(ctx);err==nil{return nil}else{
        var restoreErr error
        if existed {restoreErr=writeAtomic(path,old,0644)}else{restoreErr=os.Remove(path);if errors.Is(restoreErr,os.ErrNotExist){restoreErr=nil}}
        if restoreErr!=nil{return fmt.Errorf("Edge route update failed: %w; restore: %v",err,restoreErr)}
        if e:=m.reload(ctx);e!=nil{return fmt.Errorf("Edge route update failed: %w; previous configuration reload failed: %v",err,e)}
        return err
    }
}
func (m Manager) Export(ctx context.Context,project string) (json.RawMessage,error) {
    if !projectName.MatchString(project){return nil,errors.New("invalid project")}
    b,err:=os.ReadFile(m.routeFile(project))
    if err!=nil{return nil,err}
    return json.Marshal(string(b))
}
