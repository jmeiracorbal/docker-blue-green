package deploycli

import (
    "bytes"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
)

func TestConfigureUniformProjectManifest(t *testing.T) {
    root:=t.TempDir()
    if err:=config.Save(root,config.Document{Version:1});err!=nil{t.Fatal(err)}
    args:=[]string{"--dir",root,"--project","myapp","--host","api.example.com","--service","api","--port","8000"}
    var output bytes.Buffer
    if err:=Configure(args,strings.NewReader(""),&output);err!=nil{t.Fatal(err)}
    if err:=Configure(args,strings.NewReader(""),&output);err!=nil{t.Fatal(err)}
    cfg,err:=config.Load(root)
    if err!=nil{t.Fatal(err)}
    if len(cfg.Routes)!=1 || cfg.Project!="myapp"{t.Fatalf("non-idempotent config: %+v",cfg)}
    var exported bytes.Buffer
    if err:=Config([]string{"export","--dir",root},&exported);err!=nil{t.Fatal(err)}
    if !strings.Contains(exported.String(),`"api.example.com"`){t.Fatal(exported.String())}
    if err:=Configure([]string{"--dir",root,"--project","other"},strings.NewReader(""),&bytes.Buffer{});err==nil{
        t.Fatal("accepted changed project identity")
    }
}

func TestConfigureGuided(t *testing.T) {
    root:=t.TempDir()
    if err:=config.Save(root,config.Document{Version:1});err!=nil{t.Fatal(err)}
    answers:=strings.NewReader("myapp\napp.example.com\nweb\n3000\n")
    var prompts bytes.Buffer
    if err:=Configure([]string{"--dir",root},answers,&prompts);err!=nil{t.Fatal(err)}
    if !strings.Contains(prompts.String(),"Public hostname:"){t.Fatalf("missing prompts: %s",prompts.String())}
    cfg,err:=config.Load(root)
    if err!=nil || cfg.Routes[0].Port!=3000 {t.Fatalf("guided config: %+v %v",cfg,err)}
}

func TestIncompleteConfigRejected(t *testing.T) {
    root:=t.TempDir()
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
