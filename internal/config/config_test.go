package config

import (
    "encoding/base64"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func TestProjectConfigRoundTrip(t *testing.T) {
    root:=t.TempDir()
    cfg:=Document{
        Version:1,
        Project:"myapp",
        Routes:[]Route{{Host:"api.example.com",Service:"api",Port:8000}},
        Registry:&Registry{Host:"ghcr.io",Repository:"team/myapp",Username:"ci-publisher"},
        Deployment:&Deployment{Enabled:true,Host:"example.com",User:"deploy",Port:22,
            KnownHosts:"example.com ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("verified-host-key-for-ci"))},
        OpenBao:&OpenBao{Mount:"kv",Secrets:[]openbao.Reference{{
            Service:"api",Name:"db_password",Path:"database",Field:"password",
        }}},
    }
    if err:=Save(root,cfg);err!=nil{t.Fatal(err)}
    if err:=os.MkdirAll(filepath.Join(root,"deploy"),0755);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"deploy","compose.yml"),[]byte("services:\n  api:\n    image: nginx:1.27-alpine\n"),0644);err!=nil{t.Fatal(err)}
    if err:=SaveIngress(root,cfg.Routes);err!=nil{t.Fatal(err)}
    parsed,err:=Load(root)
    if err!=nil{t.Fatal(err)}
    if err:=parsed.Validate(true);err!=nil{t.Fatal(err)}
    if parsed.Project!="myapp" || parsed.Routes[0].Host!="api.example.com" {
        t.Fatalf("incorrect round trip: %+v",parsed)
    }
    manifest,ok:=parsed.Secrets()
    if !ok || manifest.Mount!="kv" || manifest.Secrets[0].Field!="password" {
        t.Fatal("OpenBao references not read from common config")
    }
    data,err:=parsed.Export()
    if err!=nil{t.Fatal(err)}
    if strings.Contains(string(data),"database") || strings.Contains(string(data),"db_password") ||
        !strings.Contains(string(data),"api.example.com") ||
        !strings.Contains(string(data),"ghcr.io") || !strings.Contains(string(data),"example.com") {
        t.Fatalf("public workflow export is unsafe: %s",data)
    }
}

func TestRejectsUnknownAndUnsafeConfiguration(t *testing.T) {
    cases:=[]string{
        "version: 2\nproject: demo\n",
        "version: 1\nproject: ../escape\n",
        "version: 1\nproject: demo\nsecret_value: unsafe\n",
        "version: 1\nproject: demo\nregistry:\n  host: insecure host\n  repository: company/app\n  username: deploy\n",
        "version: 1\nproject: demo\nregistry:\n  host: ghcr.io\n  repository: ../foreign\n  username: deploy\n",
        "version: 1\nproject: demo\nregistry:\n  host: ghcr.io\n  repository: company/app\n  username: deploy\n  token: leaked-credential\n",
        "version: 1\nproject: demo\ndeployment:\n  enabled: true\n  host: example.com\n  user: deploy\n  port: 22\n  known_hosts: attacker.com ssh-ed25519 eHl6eHl6eHl6eHl6eHl6\n",
        "version: 1\nproject: demo\ndeployment:\n  enabled: false\n  host: example.com\n",
        "version: 1\nproject: demo\n---\nversion: 1\n",
        "version: 1\nproject: demo\nroutes:\n - host: api.example.com\n   service: api\n   port: 80\n - host: API.EXAMPLE.COM\n   service: api\n   port: 80\n",
        "version: 1\nproject: demo\nopenbao:\n  mount: kv\n  secrets:\n    - service: api\n      name: db_pass\n      path: ../foreign\n      field: password\n",
        "version: 1\nproject: demo\nopenbao:\n  mount: kv\n  secrets:\n    - service: api\n      name: pw\n      path: db\n      field: password\n      value: do-not-store\n",
    }
    for _,raw:=range cases {
        if _,err:=Parse([]byte(raw));err==nil {t.Errorf("invalid YAML accepted: %s",raw)}
    }
}

func TestInitialRequiresConfiguration(t *testing.T) {
    cfg,err:=Parse(Initial())
    if err!=nil{t.Fatal(err)}
    if err:=cfg.Validate(true);err==nil{t.Fatal("unconfigured project accepted")}
    if _,err:=cfg.Export();err==nil{t.Fatal("exported unconfigured manifest")}
}

func TestSaveDoesNotOverwriteSymlink(t *testing.T) {
    root:=t.TempDir()
    if err:=os.MkdirAll(filepath.Join(root,"deploy"),0700);err!=nil{t.Fatal(err)}
    victim:=filepath.Join(root,"victim")
    if err:=os.WriteFile(victim,[]byte("leave-me"),0600);err!=nil{t.Fatal(err)}
    if err:=os.Symlink(victim,Path(root));err!=nil{t.Fatal(err)}
    if err:=Save(root,Document{Version:1});err==nil{t.Fatal("overwrote symlink")}
    b,err:=os.ReadFile(victim)
    if err!=nil || string(b)!="leave-me" {t.Fatalf("victim changed: %q, %v",b,err)}
}

func TestRejectsRoutesOutsideApplicationCompose(t *testing.T) {
    root:=t.TempDir()
    if err:=os.MkdirAll(filepath.Join(root,"deploy"),0755);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"deploy","compose.yml"),[]byte("services:\n  api:\n    image: nginx:latest\n    networks:\n      edge:\n        aliases: [\"${PROJECT_NAME}-${DEPLOYMENT_SLOT}-api\"]\nnetworks:\n  edge:\n    external: true\n    name: \"${DEPLOY_NETWORK}\"\n"),0644);err!=nil{
        t.Fatal(err)
    }
    cfg:=Document{Version:1,Project:"demo",Routes:[]Route{{Host:"web.example.com",Service:"web",Port:80}}}
    if err:=cfg.ValidateApplicationServices(root);err==nil{t.Fatal("accepted route to unknown service")}
    cfg.Routes[0].Service="api"
    if err:=cfg.ValidateApplicationServices(root);err!=nil{t.Fatal(err)}
}

func TestRejectLegacySplitConfiguration(t *testing.T) {
    root:=t.TempDir()
    if err:=Save(root,Document{Version:1,Project:"demo"});err!=nil{t.Fatal(err)}
    legacy:=filepath.Join(root,"deploy","secrets.json")
    if err:=os.WriteFile(legacy,[]byte("{}"),0600);err!=nil{t.Fatal(err)}
    if _,err:=Load(root);err==nil || !strings.Contains(err.Error(),"obsolete"){
        t.Fatalf("silently ignored old secrets.json: %v",err)
    }
}

func TestPublicExportRequiresRegistry(t *testing.T) {
    cfg:=Document{Version:1,Project:"demo",Routes:[]Route{{Host:"demo.example.com",Service:"app",Port:80}}}
    if _,err:=cfg.Export();err==nil{t.Fatal("workflow export accepted absent OCI configuration")}
    cfg.Registry=&Registry{Host:"ghcr.io",Repository:"demo/image",Username:"publisher"}
    exported,err:=cfg.Export()
    if err!=nil{t.Fatal(err)}
    if !strings.Contains(string(exported),`"enabled":false`){t.Fatalf("deployment unexpectedly enabled: %s",exported)}
}

func TestComposeIngressIsTheOnlyRouteAuthority(t *testing.T){
 root:=t.TempDir()
 if err:=Save(root,Document{Version:1,Project:"demo"});err!=nil{t.Fatal(err)}
 compose:=filepath.Join(root,"deploy","compose.yml")
 if err:=os.WriteFile(compose,[]byte("services:\n  api:\n    image: nginx:1.27-alpine\n"),0644);err!=nil{t.Fatal(err)}
 routes:=[]Route{{Host:"example.com",Service:"api",Port:8000}}
 if err:=SaveIngress(root,routes);err!=nil{t.Fatal(err)}
 doc,err:=Load(root)
 if err!=nil||len(doc.Routes)!=1||doc.Routes[0].Host!="example.com"{t.Fatalf("ingress load: %+v %v",doc,err)}
 if err:=SaveIngress(root,routes);err!=nil{t.Fatal(err)}
 if err:=SaveIngress(root,append(routes,Route{Host:"EXAMPLE.COM",Service:"api",Port:80}));err!=nil{t.Fatal(err)}
 if _,err:=Load(root);err==nil{t.Fatal("duplicate Compose ingress accepted")}
 if err:=os.WriteFile(Path(root),[]byte("version: 1\nproject: demo\nroutes:\n  - host: forbidden.example.com\n    service: api\n    port: 80\n"),0644);err!=nil{t.Fatal(err)}
 if _,err:=Load(root);err==nil{t.Fatal("legacy dual route ownership accepted")}
}

func TestSharedIngressRequiresInfrastructureService(t *testing.T){
 root:=t.TempDir()
 if err:=Save(root,Document{Version:1,Project:"demo"});err!=nil{t.Fatal(err)}
 compose:=filepath.Join(root,"deploy","compose.yml")
 if err:=os.WriteFile(compose,[]byte("services:\n  api:\n    image: nginx:alpine\n    networks:\n      edge:\n        aliases: [\"${PROJECT_NAME}-${DEPLOYMENT_SLOT}-api\"]\nnetworks:\n  edge:\n    external: true\n    name: \"${DEPLOY_NETWORK}\"\n"),0644);err!=nil{t.Fatal(err)}
 if err:=SaveIngress(root,[]Route{{Host:"api.example.com",Service:"api",Port:80},
     {Host:"storage.example.com",Service:"rustfs",Port:9000,Shared:true}});err!=nil{t.Fatal(err)}
 cfg,err:=Load(root);if err!=nil{t.Fatal(err)}
 if err:=cfg.ValidateApplicationServices(root);err==nil{t.Fatal("shared route accepted missing infrastructure")}
 path:=filepath.Join(root,"deploy","infrastructure","compose.yml")
 if err:=os.MkdirAll(filepath.Dir(path),0755);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(path,[]byte("services:\n  rustfs:\n    image: rustfs/rustfs:1\n    networks:\n      edge:\n        aliases: [\"${PROJECT_NAME}-infrastructure-rustfs\"]\nnetworks:\n  edge:\n    external: true\n    name: \"${DEPLOY_NETWORK}\"\n"),0644);err!=nil{t.Fatal(err)}
 if err:=cfg.ValidateApplicationServices(root);err!=nil{t.Fatal(err)}
 if err:=cfg.Validate(true);err!=nil{t.Fatal(err)}
}
