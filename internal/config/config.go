package config

import (
    "bytes"
    "encoding/base64"
    "encoding/json"
    "strconv"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "strings"

    "gopkg.in/yaml.v3"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

// Document is the single source of project-level Blue-Green settings.
// Docker Compose remains the source of services and container infrastructure;
// immutable OCI digests and release versions remain release inputs.
type Document struct {
    Version int `yaml:"version"`
    Project string `yaml:"project"`
    Routes []Route `yaml:"-"`
    Registry *Registry `yaml:"registry,omitempty"`
    CI *CI `yaml:"ci,omitempty"`
    Deployment *Deployment `yaml:"deployment,omitempty"`
    OpenBao *OpenBao `yaml:"openbao,omitempty"`
    Lifecycle *Lifecycle `yaml:"lifecycle,omitempty"`
}

type Route struct {
    Host string `yaml:"host" json:"host"`
    Service string `yaml:"service" json:"service"`
    Port int `yaml:"port" json:"port"`
    Shared bool `yaml:"shared,omitempty" json:"shared,omitempty"`
}

// Registry contains only public OCI destination data, never credentials.
type Registry struct {
    Host string `yaml:"host" json:"host"`
    Repository string `yaml:"repository" json:"repository"`
    Username string `yaml:"username" json:"username"`
}

// CI declares project-owned GitHub Actions runner labels. It contains
// scheduling configuration only; no runner credentials or provider settings.
type CI struct {
    RunnerLabels []string `yaml:"runner_labels" json:"runner_labels"`
}

// Deployment is an optional SSH target. KnownHosts contains a verified SSH
// public host key; authentication keys stay in GitHub Secrets.
type Deployment struct {
    Enabled bool `yaml:"enabled" json:"enabled"`
    Host string `yaml:"host,omitempty" json:"host,omitempty"`
    User string `yaml:"user,omitempty" json:"user,omitempty"`
    Port int `yaml:"port,omitempty" json:"port,omitempty"`
    KnownHosts string `yaml:"known_hosts,omitempty" json:"known_hosts,omitempty"`
}

// Lifecycle is a sequence of explicit, container-local one-off operations.
// Blue-Green does not interpret shell scripts or run database migrations itself.
type Lifecycle struct {
    BeforeStart []LifecycleStep `yaml:"before_start,omitempty"`
    BeforeSwitch []LifecycleStep `yaml:"before_switch,omitempty"`
}

type LifecycleStep struct {
    Service string `yaml:"service"`
    Entrypoint string `yaml:"entrypoint"`
    Args []string `yaml:"args,omitempty"`
    TimeoutSeconds int `yaml:"timeout_seconds"`
}

type OpenBao struct {
    Mount string `yaml:"mount"`
    Secrets []openbao.Reference `yaml:"secrets"`
}

var (
    projectPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
    servicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
    hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
    mountPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
    registryHostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.:-]*$`)
    registryRepoSegmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
    registryUserPattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)
    sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
    sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*package config

import (
    "bytes"
    "encoding/base64"
    "encoding/json"
    "strconv"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "strings"

    "gopkg.in/yaml.v3"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

// Document is the single source of project-level Blue-Green settings.
// Docker Compose remains the source of services and container infrastructure;
// immutable OCI digests and release versions remain release inputs.
type Document struct {
    Version int `yaml:"version"`
    Project string `yaml:"project"`
    Routes []Route `yaml:"-"`
    Registry *Registry `yaml:"registry,omitempty"`
    CI *CI `yaml:"ci,omitempty"`
    Deployment *Deployment `yaml:"deployment,omitempty"`
    OpenBao *OpenBao `yaml:"openbao,omitempty"`
    Lifecycle *Lifecycle `yaml:"lifecycle,omitempty"`
}

type Route struct {
    Host string `yaml:"host" json:"host"`
    Service string `yaml:"service" json:"service"`
    Port int `yaml:"port" json:"port"`
    Shared bool `yaml:"shared,omitempty" json:"shared,omitempty"`
}

// Registry contains only public OCI destination data, never credentials.
type Registry struct {
    Host string `yaml:"host" json:"host"`
    Repository string `yaml:"repository" json:"repository"`
    Username string `yaml:"username" json:"username"`
}

// CI declares project-owned GitHub Actions runner labels. It contains
// scheduling configuration only; no runner credentials or provider settings.
type CI struct {
    RunnerLabels []string `yaml:"runner_labels" json:"runner_labels"`
}

// Deployment is an optional SSH target. KnownHosts contains a verified SSH
// public host key; authentication keys stay in GitHub Secrets.
type Deployment struct {
    Enabled bool `yaml:"enabled" json:"enabled"`
    Host string `yaml:"host,omitempty" json:"host,omitempty"`
    User string `yaml:"user,omitempty" json:"user,omitempty"`
    Port int `yaml:"port,omitempty" json:"port,omitempty"`
    KnownHosts string `yaml:"known_hosts,omitempty" json:"known_hosts,omitempty"`
}

// Lifecycle is a sequence of explicit, container-local one-off operations.
// Blue-Green does not interpret shell scripts or run database migrations itself.
type Lifecycle struct {
    BeforeStart []LifecycleStep `yaml:"before_start,omitempty"`
    BeforeSwitch []LifecycleStep `yaml:"before_switch,omitempty"`
}

type LifecycleStep struct {
    Service string `yaml:"service"`
    Entrypoint string `yaml:"entrypoint"`
    Args []string `yaml:"args,omitempty"`
    TimeoutSeconds int `yaml:"timeout_seconds"`
}

type OpenBao struct {
    Mount string `yaml:"mount"`
    Secrets []openbao.Reference `yaml:"secrets"`
}

var (
    projectPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
    servicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
    hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
    mountPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
    registryHostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.:-]*$`)
    registryRepoSegmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
    registryUserPattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)
    sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
)
    runnerLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}package config

import (
    "bytes"
    "encoding/base64"
    "encoding/json"
    "strconv"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "strings"

    "gopkg.in/yaml.v3"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

// Document is the single source of project-level Blue-Green settings.
// Docker Compose remains the source of services and container infrastructure;
// immutable OCI digests and release versions remain release inputs.
type Document struct {
    Version int `yaml:"version"`
    Project string `yaml:"project"`
    Routes []Route `yaml:"-"`
    Registry *Registry `yaml:"registry,omitempty"`
    CI *CI `yaml:"ci,omitempty"`
    Deployment *Deployment `yaml:"deployment,omitempty"`
    OpenBao *OpenBao `yaml:"openbao,omitempty"`
    Lifecycle *Lifecycle `yaml:"lifecycle,omitempty"`
}

type Route struct {
    Host string `yaml:"host" json:"host"`
    Service string `yaml:"service" json:"service"`
    Port int `yaml:"port" json:"port"`
    Shared bool `yaml:"shared,omitempty" json:"shared,omitempty"`
}

// Registry contains only public OCI destination data, never credentials.
type Registry struct {
    Host string `yaml:"host" json:"host"`
    Repository string `yaml:"repository" json:"repository"`
    Username string `yaml:"username" json:"username"`
}

// CI declares project-owned GitHub Actions runner labels. It contains
// scheduling configuration only; no runner credentials or provider settings.
type CI struct {
    RunnerLabels []string `yaml:"runner_labels" json:"runner_labels"`
}

// Deployment is an optional SSH target. KnownHosts contains a verified SSH
// public host key; authentication keys stay in GitHub Secrets.
type Deployment struct {
    Enabled bool `yaml:"enabled" json:"enabled"`
    Host string `yaml:"host,omitempty" json:"host,omitempty"`
    User string `yaml:"user,omitempty" json:"user,omitempty"`
    Port int `yaml:"port,omitempty" json:"port,omitempty"`
    KnownHosts string `yaml:"known_hosts,omitempty" json:"known_hosts,omitempty"`
}

// Lifecycle is a sequence of explicit, container-local one-off operations.
// Blue-Green does not interpret shell scripts or run database migrations itself.
type Lifecycle struct {
    BeforeStart []LifecycleStep `yaml:"before_start,omitempty"`
    BeforeSwitch []LifecycleStep `yaml:"before_switch,omitempty"`
}

type LifecycleStep struct {
    Service string `yaml:"service"`
    Entrypoint string `yaml:"entrypoint"`
    Args []string `yaml:"args,omitempty"`
    TimeoutSeconds int `yaml:"timeout_seconds"`
}

type OpenBao struct {
    Mount string `yaml:"mount"`
    Secrets []openbao.Reference `yaml:"secrets"`
}

var (
    projectPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
    servicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
    hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
    mountPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
    registryHostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.:-]*$`)
    registryRepoSegmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
    registryUserPattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)
    sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
)
)

func Path(root string) string {
    return filepath.Join(root, "deploy", "bluegreen.yml")
}

func Initial() []byte {
    return []byte("version: 1\nproject: \"\"\n")
}

func Parse(data []byte) (Document, error) {
    d := yaml.NewDecoder(bytes.NewReader(data))
    d.KnownFields(true)
    var cfg Document
    if err := d.Decode(&cfg); err != nil {
        return Document{}, fmt.Errorf("parse deploy/bluegreen.yml: %w", err)
    }
    var trailing any
    if err := d.Decode(&trailing); err != io.EOF {
        return Document{}, errors.New("deploy/bluegreen.yml must contain exactly one YAML document")
    }
    if err := cfg.Validate(false); err != nil {
        return Document{}, err
    }
    return cfg, nil
}

func Load(root string) (Document, error) {
    // Fail closed when previous split configuration exists. Ignoring an old
    // secrets.json could silently launch containers without expected secrets.
    for _,legacy:=range []string{"routes.json","secrets.json"} {
        file:=filepath.Join(root,"deploy",legacy)
        if _,err:=os.Lstat(file);err==nil{
            return Document{},fmt.Errorf("obsolete deployment configuration %s; move its settings into deploy/bluegreen.yml",file)
        }else if !errors.Is(err,os.ErrNotExist){
            return Document{},err
        }
    }
    data, err := os.ReadFile(Path(root))
    if err != nil {
        return Document{}, fmt.Errorf("read deploy/bluegreen.yml: %w", err)
    }
    cfg,err:=Parse(data)
    if err!=nil{return Document{},err}
    routes,err:=LoadIngress(root)
    if err!=nil{return Document{},err}
    cfg.Routes=routes
    if err:=cfg.Validate(false);err!=nil{return Document{},err}
    return cfg,nil
}


type composeIngress struct {
    XBluegreen *struct{
        Ingress []Route `yaml:"ingress"`
    } `yaml:"x-bluegreen"`
}

// LoadIngress reads the one authoritative public route declaration from
// deploy/compose.yml. Route values are never duplicated in bluegreen.yml.
func LoadIngress(root string)([]Route,error){
    source:=filepath.Join(root,"deploy","compose.yml")
    b,err:=os.ReadFile(source)
    if err!=nil{return nil,fmt.Errorf("read application Compose ingress: %w",err)}
    var model composeIngress
    if err=yaml.Unmarshal(b,&model);err!=nil{return nil,fmt.Errorf("read x-bluegreen.ingress: %w",err)}
    if model.XBluegreen==nil{return nil,nil}
    return model.XBluegreen.Ingress,nil
}

// SaveIngress updates only the project-level Compose ingress extension.
// It preserves other mapping nodes, including services and networks.
func SaveIngress(root string,routes []Route) error {
    path:=filepath.Join(root,"deploy","compose.yml")
    b,err:=os.ReadFile(path)
    if err!=nil{return err}
    var document yaml.Node
    if err=yaml.Unmarshal(b,&document);err!=nil{return err}
    if len(document.Content)!=1 || document.Content[0].Kind!=yaml.MappingNode {
        return errors.New("Compose must be a YAML mapping")
    }
    mapping:=document.Content[0]
    var ext *yaml.Node
    for i:=0;i+1<len(mapping.Content);i+=2 {
        if mapping.Content[i].Value=="x-bluegreen"{ext=mapping.Content[i+1];break}
    }
    if ext==nil{
        ext=&yaml.Node{Kind:yaml.MappingNode,Tag:"!!map"}
        mapping.Content=append(mapping.Content,&yaml.Node{Kind:yaml.ScalarNode,Tag:"!!str",Value:"x-bluegreen"},ext)
    }
    if ext.Kind!=yaml.MappingNode{return errors.New("x-bluegreen must be a mapping")}
    if routes==nil {routes=[]Route{}}
    routeBytes,err:=yaml.Marshal(routes)
    if err!=nil{return err}
    var seq yaml.Node
    if err=yaml.Unmarshal(routeBytes,&seq);err!=nil{return err}
    if len(seq.Content)!=1 || seq.Content[0].Kind!=yaml.SequenceNode {
        return errors.New("invalid Compose ingress sequence")
    }
    found:=false
    for i:=0;i+1<len(ext.Content);i+=2 {
        if ext.Content[i].Value=="ingress" {ext.Content[i+1]=seq.Content[0];found=true;break}
    }
    if !found{
        ext.Content=append(ext.Content,&yaml.Node{Kind:yaml.ScalarNode,Tag:"!!str",Value:"ingress"},seq.Content[0])
    }
    updated,err:=yaml.Marshal(&document)
    if err!=nil{return err}
    return atomicYAML(path,updated)
}
func atomicYAML(path string,content []byte)error{
    info,err:=os.Lstat(path)
    if err==nil && !info.Mode().IsRegular(){return errors.New("refusing to replace nonregular YAML file")}
    if err!=nil && !errors.Is(err,os.ErrNotExist){return err}
    if err=os.MkdirAll(filepath.Dir(path),0750);err!=nil{return err}
    f,err:=os.CreateTemp(filepath.Dir(path),".bluegreen-yaml-")
    if err!=nil{return err}
    defer os.Remove(f.Name())
    if err=f.Chmod(0644);err!=nil{f.Close();return err}
    if _,err=f.Write(content);err!=nil{f.Close();return err}
    if err=f.Sync();err!=nil{f.Close();return err}
    if err=f.Close();err!=nil{return err}
    return os.Rename(f.Name(),path)
}

// Validate allows unfinished init scaffolds only when complete=false.
// Before deployment or workflow export, every project must have an identity
// and at least one validated route. Never fall back to removed JSON manifests.
func (cfg Document) Validate(complete bool) error {
    if cfg.Version != 1 {
        return errors.New("unsupported bluegreen configuration version (expected 1)")
    }
    if cfg.Project == "" {
        if complete { return errors.New("configure the project with 'bluegreen configure'") }
    } else if !projectPattern.MatchString(cfg.Project) {
        return errors.New("invalid bluegreen project identifier")
    }
    if complete && len(cfg.Routes) == 0 {
        return errors.New("configure at least one public route")
    }
    seen := map[string]bool{}
    for _, route := range cfg.Routes {
        if !hostPattern.MatchString(route.Host) || strings.Contains(route.Host, "..") {
            return fmt.Errorf("invalid public route host %q", route.Host)
        }
        if !servicePattern.MatchString(route.Service) || route.Port < 1 || route.Port > 65535 {
            return errors.New("invalid route service or port")
        }
        key := strings.ToLower(route.Host)
        if seen[key] { return fmt.Errorf("duplicate public route host %q", route.Host) }
        seen[key] = true
    }
    if cfg.Registry != nil {
        r:=cfg.Registry
        if !registryHostPattern.MatchString(r.Host) || strings.Contains(r.Host,"..") ||
           !(strings.ContainsAny(r.Host,".:") || r.Host=="localhost") {
            return errors.New("invalid OCI registry hostname")
        }
        if len(r.Repository)>255 || strings.HasSuffix(r.Repository,"/") || strings.Contains(r.Repository,"//") {
            return errors.New("invalid OCI registry repository")
        }
        for _,part:=range strings.Split(r.Repository,"/") {
            if !registryRepoSegmentPattern.MatchString(part) || part=="." || part==".." {
                return errors.New("invalid OCI registry repository")
            }
        }
        if !registryUserPattern.MatchString(r.Username) || len(r.Username)>128 {
            return errors.New("invalid OCI registry username")
        }
    }
    if cfg.CI != nil {
        labels:=cfg.CI.RunnerLabels
        if len(labels)==0 || len(labels)>16 {
            return errors.New("ci.runner_labels requires between 1 and 16 labels")
        }
        seenLabels:=make(map[string]bool,len(labels))
        for _,label:=range labels {
            if !runnerLabelPattern.MatchString(label) {
                return fmt.Errorf("invalid ci.runner_labels entry %q",label)
            }
            key:=strings.ToLower(label)
            if seenLabels[key] {
                return fmt.Errorf("duplicate ci.runner_labels entry %q",label)
            }
            seenLabels[key]=true
        }
    }
    if cfg.Deployment != nil {
        dep:=cfg.Deployment
        if !dep.Enabled {
            if dep.Host!="" || dep.User!="" || dep.Port!=0 || dep.KnownHosts!="" {
                return errors.New("disabled deployment must not retain SSH settings")
            }
        } else {
            if !sshHostPattern.MatchString(dep.Host) || strings.Contains(dep.Host,"..") ||
                !sshUserPattern.MatchString(dep.User) || dep.Port<1 || dep.Port>65535 {
                return errors.New("invalid deployment SSH host, user or port")
            }
            parts:=strings.Fields(dep.KnownHosts)
            expected:=dep.Host
            if dep.Port!=22 { expected="["+dep.Host+"]:"+strconv.Itoa(dep.Port) }
            if len(parts)!=3 || parts[0]!=expected ||
               (parts[1]!="ssh-ed25519" && parts[1]!="ssh-rsa" && !strings.HasPrefix(parts[1],"ecdsa-sha2-")) ||
               strings.ContainsAny(dep.KnownHosts,"\n\r") {
                return errors.New("deployment known_hosts must contain the verified SSH public host key")
            }
            pub,err:=base64.StdEncoding.DecodeString(parts[2])
            if err!=nil || len(pub)<16 { return errors.New("invalid deployment SSH public host key") }
        }
    }
    if cfg.Lifecycle != nil {
        if len(cfg.Lifecycle.BeforeStart)>12 || len(cfg.Lifecycle.BeforeSwitch)>12 {
            return errors.New("lifecycle permits at most 12 operations per stage")
        }
        for stage, steps := range map[string][]LifecycleStep{
            "before_start": cfg.Lifecycle.BeforeStart, "before_switch": cfg.Lifecycle.BeforeSwitch,
        } {
            for i, step := range steps {
                if !servicePattern.MatchString(step.Service) {
                    return fmt.Errorf("lifecycle %s[%d]: invalid Compose service",stage,i)
                }
                if step.Entrypoint=="" || len(step.Entrypoint)>256 ||
                    strings.ContainsAny(step.Entrypoint," \t\r\n\x00") ||
                    strings.HasPrefix(step.Entrypoint,"-") {
                    return fmt.Errorf("lifecycle %s[%d]: invalid executable",stage,i)
                }
                if step.TimeoutSeconds<1 || step.TimeoutSeconds>3600 {
                    return fmt.Errorf("lifecycle %s[%d]: timeout_seconds must be 1..3600",stage,i)
                }
                if len(step.Args)>40 { return fmt.Errorf("lifecycle %s[%d]: too many arguments",stage,i) }
                for _,arg:=range step.Args {
                    if len(arg)>1024 || strings.ContainsAny(arg,"\x00\r\n") {
                        return fmt.Errorf("lifecycle %s[%d]: invalid argument",stage,i)
                    }
                }
            }
        }
    }
    if cfg.OpenBao != nil {
        if !mountPattern.MatchString(cfg.OpenBao.Mount) {
            return errors.New("OpenBao KV v2 mount must be configured")
        }
        if len(cfg.OpenBao.Secrets) > 0 {
            if cfg.Project == "" { return errors.New("configure project before adding OpenBao secrets") }
            m := openbao.Manifest{Mount: cfg.OpenBao.Mount, Secrets: cfg.OpenBao.Secrets}
            if err := m.Validate(cfg.Project); err != nil { return err }
        }
    }
    return nil
}

func Save(root string, cfg Document) error {
    if err := cfg.Validate(false); err != nil { return err }
    content, err := yaml.Marshal(cfg)
    if err != nil { return err }
    return atomicYAML(Path(root),content)
}

// Export is the validated public workflow contract. No OpenBao references
// or authentication credentials are allowed in this JSON.
func (cfg Document) Export() ([]byte, error) {
    if err := cfg.Validate(true); err != nil { return nil, err }
    if cfg.Registry==nil {
        return nil, errors.New("configure OCI registry before publishing a release")
    }
    deployment:=Deployment{Enabled:false}
    if cfg.Deployment!=nil { deployment=*cfg.Deployment }
    return json.Marshal(struct {
        Project string `json:"project"`
        Routes []Route `json:"routes"`
        Registry Registry `json:"registry"`
        Deployment Deployment `json:"deployment"`
        CI *CI `json:"ci,omitempty"`
    }{Project:cfg.Project, Routes:cfg.Routes,Registry:*cfg.Registry,Deployment:deployment,CI:cfg.CI})
}

func (cfg Document) Secrets() (openbao.Manifest, bool) {
    if cfg.OpenBao == nil || len(cfg.OpenBao.Secrets)==0 { return openbao.Manifest{}, false }
    return openbao.Manifest{Mount: cfg.OpenBao.Mount, Secrets:cfg.OpenBao.Secrets}, true
}

// ValidateApplicationServices checks that all configured routes and secret
// associations point to actual application services in generated Compose.
// Stateful infrastructure is not part of the Blue-Green application slots.
func (cfg Document) ValidateApplicationServices(root string) error {
    data,err:=os.ReadFile(filepath.Join(root,"deploy","compose.yml"))
    if err!=nil{return fmt.Errorf("read application Compose: %w",err)}
    var model struct {
        Services map[string]any `yaml:"services"`
    }
    if err:=yaml.Unmarshal(data,&model);err!=nil{
        return fmt.Errorf("parse application Compose: %w",err)
    }
    if len(model.Services)==0{return errors.New("application Compose has no services")}
    for _,route:=range cfg.Routes{
        if route.Shared {
            infraPath:=filepath.Join(root,"deploy","infrastructure","compose.yml")
            b,err:=os.ReadFile(infraPath)
            if err!=nil{return fmt.Errorf("read infrastructure for shared ingress: %w",err)}
            var infra struct {Services map[string]any `yaml:"services"`}
            if err:=yaml.Unmarshal(b,&infra);err!=nil{return err}
            if _,ok:=infra.Services[route.Service];!ok {
                return fmt.Errorf("shared ingress %q refers to unknown infrastructure service %q",route.Host,route.Service)
            }
            if err:=validateSharedIngressNetwork(b,cfg.Project,route.Service);err!=nil{return err}
        }else{
            if _,ok:=model.Services[route.Service];!ok {
                return fmt.Errorf("route %q refers to an unknown application service %q",route.Host,route.Service)
            }
            if err:=validateApplicationIngressNetwork(data,route.Service);err!=nil{return err}
        }
    }
    if cfg.Lifecycle!=nil {
        for _,step:=range append(append([]LifecycleStep(nil),cfg.Lifecycle.BeforeStart...),cfg.Lifecycle.BeforeSwitch...) {
            if _,ok:=model.Services[step.Service];!ok {
                return fmt.Errorf("lifecycle refers to unknown application Compose service %q",step.Service)
            }
        }
    }
    if cfg.OpenBao!=nil{
        for _,ref:=range cfg.OpenBao.Secrets{
            if _,ok:=model.Services[ref.Service];!ok{
                return fmt.Errorf("OpenBao secret %q refers to an unknown application service %q",ref.Name,ref.Service)
            }
        }
    }
    return nil
}

// ValidateLifecycleServices requires the hook service to be part of the
// immutable release image set. This prevents unpinned auxiliary services from
// being executed as deployment hooks.
func (cfg Document) ValidateLifecycleServices(images map[string]string) error {
    if cfg.Lifecycle==nil{return nil}
    for _,step:=range append(append([]LifecycleStep(nil),cfg.Lifecycle.BeforeStart...),cfg.Lifecycle.BeforeSwitch...) {
        if _,ok:=images[step.Service];!ok{
            return fmt.Errorf("lifecycle service %q has no immutable release image",step.Service)
        }
    }
    return nil
}

// ValidateInfrastructureServices disallows a second Caddy instance within
// application infrastructure. Only the host-wide Edge owns the proxy.
func (cfg Document) ValidateInfrastructureServices(root string) error {
    path:=filepath.Join(root,"deploy","infrastructure","compose.yml")
    data,err:=os.ReadFile(path)
    if errors.Is(err,os.ErrNotExist){return nil}
    if err!=nil{return err}
    var model struct { Services map[string]any `yaml:"services"` }
    if err:=yaml.Unmarshal(data,&model);err!=nil{return fmt.Errorf("parse project infrastructure Compose: %w",err)}
    if _,found:=model.Services["caddy"];found{return errors.New("per-project Caddy is forbidden; use shared Edge")}
    return nil
}

func validateSharedIngressNetwork(data []byte,project,service string)error{
    var model struct{
        Services map[string]any `yaml:"services"`
        Networks map[string]any `yaml:"networks"`
    }
    if err:=yaml.Unmarshal(data,&model);err!=nil{return err}
    raw,ok:=model.Services[service].(map[string]any)
    if !ok{return errors.New("shared ingress service must be a mapping")}
    networks,ok:=raw["networks"].(map[string]any)
    if !ok{return fmt.Errorf("shared ingress service %q must declare an Edge network alias",service)}
    expected:=project+"-infrastructure-"+service
    variable:="${PROJECT_NAME}-infrastructure-"+service
    for name,spec:=range networks{
        top,ok:=model.Networks[name].(map[string]any)
        if !ok||top["external"]!=true||top["name"]!="${DEPLOY_NETWORK}" {continue}
        net,ok:=spec.(map[string]any)
        if !ok {continue}
        aliases,ok:=net["aliases"].([]any)
        if !ok{continue}
        for _,alias:=range aliases {
            if alias==expected||alias==variable{return nil}
        }
    }
    return fmt.Errorf("shared ingress service %q must join external DEPLOY_NETWORK with alias %s",service,expected)
}

func validateApplicationIngressNetwork(data []byte,service string)error{
    var model struct {
        Services map[string]any `yaml:"services"`
        Networks map[string]any `yaml:"networks"`
    }
    if err:=yaml.Unmarshal(data,&model);err!=nil{return err}
    svc,ok:=model.Services[service].(map[string]any)
    if !ok {return errors.New("ingress service must be a mapping")}
    networks,ok:=svc["networks"].(map[string]any)
    if !ok{return fmt.Errorf("ingress service %q must declare the Edge network",service)}
    expected:="${PROJECT_NAME}-${DEPLOYMENT_SLOT}-"+service
    for name,spec:=range networks {
        top,ok:=model.Networks[name].(map[string]any)
        if !ok || top["external"]!=true || top["name"]!="${DEPLOY_NETWORK}" {continue}
        settings,ok:=spec.(map[string]any)
        if !ok{continue}
        aliases,ok:=settings["aliases"].([]any)
        if !ok{continue}
        for _,alias:=range aliases {
            if alias==expected{return nil}
        }
    }
    return fmt.Errorf("ingress service %q must join external DEPLOY_NETWORK with slot-aware alias %s",service,expected)
}
