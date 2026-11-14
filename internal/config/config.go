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
    Routes []Route `yaml:"routes"`
    Registry *Registry `yaml:"registry,omitempty"`
    Deployment *Deployment `yaml:"deployment,omitempty"`
    OpenBao *OpenBao `yaml:"openbao,omitempty"`
}

type Route struct {
    Host string `yaml:"host" json:"host"`
    Service string `yaml:"service" json:"service"`
    Port int `yaml:"port" json:"port"`
}

// Registry contains only public OCI destination data, never credentials.
type Registry struct {
    Host string `yaml:"host" json:"host"`
    Repository string `yaml:"repository" json:"repository"`
    Username string `yaml:"username" json:"username"`
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
    sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
)

func Path(root string) string {
    return filepath.Join(root, "deploy", "bluegreen.yml")
}

func Initial() []byte {
    return []byte("version: 1\nproject: \"\"\nroutes: []\n")
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
    return Parse(data)
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
    dest := Path(root)
    info, err := os.Lstat(dest)
    if err == nil && !info.Mode().IsRegular() {
        return errors.New("refusing to replace nonregular bluegreen configuration")
    }
    if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
    if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil { return err }
    f, err := os.CreateTemp(filepath.Dir(dest), ".bluegreen-config-")
    if err != nil { return err }
    defer os.Remove(f.Name())
    if err := f.Chmod(0644); err != nil { f.Close(); return err }
    if _, err := f.Write(content); err != nil { f.Close(); return err }
    if err := f.Sync(); err != nil { f.Close(); return err }
    if err := f.Close(); err != nil { return err }
    return os.Rename(f.Name(), dest)
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
    }{Project:cfg.Project, Routes:cfg.Routes,Registry:*cfg.Registry,Deployment:deployment})
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
        if _,ok:=model.Services[route.Service];!ok{
            return fmt.Errorf("route %q refers to an unknown application service %q",route.Host,route.Service)
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
