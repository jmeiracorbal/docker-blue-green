package deploycli

import (
    "bytes"
    "encoding/base64"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
)

func prepareProject(t *testing.T,root string,service string) {
    t.Helper()
    if err:=os.MkdirAll(filepath.Join(root,"deploy"),0755);err!=nil{t.Fatal(err)}
    compose:="services:\n  "+service+":\n    image: nginx:1.27-alpine\n"
    if err:=os.WriteFile(filepath.Join(root,"deploy","compose.yml"),[]byte(compose),0644);err!=nil{t.Fatal(err)}
}

func TestConfigureUniformProjectManifest(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"api")
    if err:=config.Save(root,config.Document{Version:1});err!=nil{t.Fatal(err)}
    args:=[]string{"--dir",root,"--project","myapp","--host","api.example.com","--service","api","--port","8000",
        "--registry-host","ghcr.io","--registry-repository","team/myapp","--registry-username","ci-publisher"}
    var output bytes.Buffer
    if err:=Configure(args,strings.NewReader(""),&output);err!=nil{t.Fatal(err)}
    if err:=Configure(args,strings.NewReader(""),&output);err!=nil{t.Fatal(err)}
    cfg,err:=config.Load(root)
    if err!=nil{t.Fatal(err)}
    if len(cfg.Routes)!=1 || cfg.Project!="myapp" || cfg.Registry==nil || cfg.Registry.Host!="ghcr.io"{
        t.Fatalf("non-idempotent config: %+v",cfg)
    }
    var exported bytes.Buffer
    if err:=Config([]string{"export","--dir",root},&exported);err!=nil{t.Fatal(err)}
    if !strings.Contains(exported.String(),`"api.example.com"`){t.Fatal(exported.String())}
    if err:=Configure([]string{"--dir",root,"--project","other"},strings.NewReader(""),&bytes.Buffer{});err==nil{
        t.Fatal("accepted changed project identity")
    }
}

func TestConfigureGuided(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"web")
    if err:=config.Save(root,config.Document{Version:1});err!=nil{t.Fatal(err)}
    answers:=strings.NewReader("myapp\napp.example.com\nweb\n3000\nghcr.io\nteam/myapp\npublisher\nno\n")
    var prompts bytes.Buffer
    if err:=Configure([]string{"--dir",root},answers,&prompts);err!=nil{t.Fatal(err)}
    if !strings.Contains(prompts.String(),"Public hostname:"){t.Fatalf("missing prompts: %s",prompts.String())}
    cfg,err:=config.Load(root)
    if err!=nil || cfg.Routes[0].Port!=3000 {t.Fatalf("guided config: %+v %v",cfg,err)}
}

func TestIncompleteConfigRejected(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"api")
    if err:=config.Save(root,config.Document{Version:1});err!=nil{t.Fatal(err)}
    if err:=Config([]string{"check","--dir",root},&bytes.Buffer{});err==nil{
        t.Fatal("accepted unfinished configuration")
    }
    if err:=Configure([]string{"--dir",root,"--project","myapp","--host","invalid..host",
        "--service","api","--port","8000"},strings.NewReader(""),&bytes.Buffer{});err==nil {
        t.Fatal("accepted invalid route")
    }
    cfg,err:=config.Load(root)
    if err!=nil || cfg.Project!=""{t.Fatal("failed configure changed the project")}
}

func TestConfigureRemoteDeploymentAndDisable(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"api")
    if err:=config.Save(root,config.Document{Version:1,Project:"demo",Routes:[]config.Route{
        {Host:"api.example.com",Service:"api",Port:8000}},
        Registry:&config.Registry{Host:"ghcr.io",Repository:"team/demo",Username:"publisher"}});err!=nil{t.Fatal(err)}
    knownHost:="deploy.example.com ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("verified-public-host-key-fixture"))
    args:=[]string{"--dir",root,"--deploy-host","deploy.example.com","--deploy-user","deploy",
        "--deploy-port","22","--deploy-known-hosts",knownHost}
    if err:=Configure(args,strings.NewReader(""),&bytes.Buffer{});err!=nil{t.Fatal(err)}
    cfg,err:=config.Load(root)
    if err!=nil || cfg.Deployment==nil || !cfg.Deployment.Enabled {t.Fatalf("remote config not saved: %+v %v",cfg,err)}
    if err:=Configure([]string{"--dir",root,"--deploy-disabled"},strings.NewReader(""),&bytes.Buffer{});err!=nil{t.Fatal(err)}
    cfg,err=config.Load(root)
    if err!=nil || cfg.Deployment!=nil{t.Fatal("remote deployment not disabled",err)}
}

func TestRejectPartialRegistryAndRemoteFlags(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"api")
    if err:=config.Save(root,config.Document{Version:1,Project:"demo",
        Routes:[]config.Route{{Host:"api.example.com",Service:"api",Port:8000}}});err!=nil{t.Fatal(err)}
    for _,args:=range [][]string{
        {"--dir",root,"--registry-host","ghcr.io"},
        {"--dir",root,"--deploy-host","example.com"},
        {"--dir",root,"--deploy-disabled","--deploy-host","example.com"},
    } {
        if err:=Configure(args,strings.NewReader(""),&bytes.Buffer{});err==nil {
            t.Fatalf("accepted partial configuration: %v",args)
        }
    }
}
