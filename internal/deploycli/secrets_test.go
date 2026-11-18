package deploycli

import (
    "encoding/base64"
    "bufio"
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func TestSecretCheckUsesManagedProxyEndpoint(t *testing.T) {
    root:=t.TempDir()
    cfg:=config.Document{Version:1,Project:"bluegreenclitest001",
        Routes:[]config.Route{{Host:"api.example.com",Service:"api",Port:80}},
        OpenBao:&config.OpenBao{Mount:"kv",Secrets:[]openbao.Reference{{
            Service:"api",Name:"database_password",Path:"database",Field:"password",
        }}},
    }
    if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
    prepareProject(t,root,"api")
    if err:=config.SaveIngress(root,cfg.Routes);err!=nil{t.Fatal(err)}
    var output bytes.Buffer
    err:=checkSecrets([]string{"check","--dir",root},&output)
    if err==nil || !strings.Contains(err.Error(),"access OpenBao Proxy socket"){
        t.Fatalf("expected unprovisioned proxy error: %v",err)
    }
    if output.Len()!=0{t.Fatal("unexpected output")}
}

func TestSecretCheckRejectsSocketFlag(t *testing.T) {
    err := checkSecrets([]string{"check", "--project", "myapp", "--socket", "/tmp/proxy.sock"}, &bytes.Buffer{})
    if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
        t.Fatalf("socket override should be rejected: %v", err)
    }
}

func TestSecretAddCreatesAndExtendsManifest(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"api")
    if err:=config.Save(root,config.Document{Version:1,Project:"myapp",
        Routes:[]config.Route{{Host:"api.example.com",Service:"api",Port:80}},
    });err!=nil{t.Fatal(err)}
    if err:=config.SaveIngress(root,[]config.Route{{Host:"api.example.com",Service:"api",Port:80}});err!=nil{t.Fatal(err)}
    args:=[]string{"add","--dir",root,"--project","myapp","--mount","kv","--service","api",
        "--name","database_password","--path","database","--field","password"}
    var out bytes.Buffer
    if err:=checkSecrets(args,&out);err!=nil{t.Fatal(err)}
    if err:=checkSecrets(args,&out);err!=nil{t.Fatal(err)}
    cfg,err:=config.Load(root)
    if err!=nil || cfg.OpenBao==nil || len(cfg.OpenBao.Secrets)!=1{t.Fatalf("config: %+v %v",cfg,err)}
    second:=[]string{"add","--dir",root,"--service","api","--name","api_token","--path","auth","--field","token"}
    if err:=checkSecrets(second,&out);err!=nil{t.Fatal(err)}
    cfg,err=config.Load(root)
    if err!=nil || len(cfg.OpenBao.Secrets)!=2{t.Fatalf("extended config: %+v %v",cfg,err)}
    if err:=checkSecrets([]string{"add","--dir",root,"--service","api","--name","api_token","--path","other","--field","token"},&out);err==nil{
        t.Fatal("overwrote existing secret reference")
    }
    if _,err:=os.Stat(filepath.Join(root,"deploy","secrets.json"));!os.IsNotExist(err){
        t.Fatal("legacy secrets.json must not be generated")
    }
}
func TestGuidedOpenBaoSetup(t *testing.T) {
    answers:=strings.NewReader("https://openbao.example.com:8200\ndeploy\n/secure/role_id\n/secure/secret_id\n")
    var prompts bytes.Buffer
    p,err:=promptProxyProvision("myapp",answers,&prompts)
    if err!=nil{t.Fatal(err)}
    if p.Project!="myapp" || p.ServerURL!="https://openbao.example.com:8200" || p.DeployUser!="deploy" ||
        p.RoleIDFile!="/secure/role_id" || p.SecretIDFile!="/secure/secret_id" {
        t.Fatalf("incorrect wizard result: %+v",p)
    }
    if !strings.Contains(prompts.String(),"AppRole") {t.Fatal("setup prompts are incomplete")}
    if _,err:=promptProxyProvision("myapp",strings.NewReader("http://insecure\ndeploy\n/role\n/secret\n"),&bytes.Buffer{});err==nil{
        t.Fatal("accepted insecure OpenBao server")
    }
}

func TestDiscoveryRequiresAuthorizedKVScope(t *testing.T) {
    root:=t.TempDir()
    prepareProject(t,root,"api")
    if err:=config.Save(root,config.Document{Version:1,Project:"myapp"});err!=nil{t.Fatal(err)}
    var out bytes.Buffer
    err:=checkSecrets([]string{"list","--dir",root},&out)
    if err==nil || !strings.Contains(err.Error(),"--mount"){t.Fatalf("KV mount was guessed: %v",err)}
    if out.Len()!=0{t.Fatal("unexpected discovery output")}
    err=checkSecrets([]string{"fields","--project","../foreign","--dir",root,"--mount","kv","--path","database"},&out)
    if err==nil{t.Fatal("accepted cross-project scope")}
}
func TestChooseKVPathAndField(t *testing.T) {
    reader:=bufio.NewReader(strings.NewReader("2\n1\n"))
    var out bytes.Buffer
    selected,err:=chooseKVName(reader,&out,"KV secret",[]string{"backend","database"})
    if err!=nil||selected!="database"{t.Fatalf("selected path %q: %v",selected,err)}
    selected,err=chooseKVName(reader,&out,"KV field",[]string{"password","username"})
    if err!=nil||selected!="password"{t.Fatalf("selected field %q: %v",selected,err)}
    if !strings.Contains(out.String(),"Select KV secret"){t.Fatal("no selection prompt")}
    if _,err:=chooseKVName(bufio.NewReader(strings.NewReader("3\n")),&bytes.Buffer{},"KV field",[]string{"password"});err==nil{
        t.Fatal("accepted invalid selection")
    }
    if _,err:=chooseKVName(bufio.NewReader(strings.NewReader("1\n")),&bytes.Buffer{},"KV secret",nil);err==nil{
        t.Fatal("accepted missing KV keys")
    }
}

func TestSecretPlanIsNonMutatingAndNeedsNoOpenBaoConnection(t *testing.T){
    root:=t.TempDir()
    prepareProject(t,root,"api")
    cfg:=config.Document{Version:1,Project:"myapp",
        OpenBao:&config.OpenBao{Mount:"kv",Secrets:[]openbao.Reference{{
            Service:"api",Name:"database_password",Path:"database",Field:"password",
        }}},
    }
    if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
    if err:=config.SaveIngress(root,[]config.Route{{Host:"api.example.com",Service:"api",Port:80}});err!=nil{t.Fatal(err)}
    var out bytes.Buffer
    if err:=checkSecrets([]string{"plan","--dir",root},&out);err!=nil{t.Fatal(err)}
    got:=out.String()
    for _,expect:=range []string{`"project": "myapp"`,`"openbao_mount": "kv"`,`"path": "database"`,`"field": "password"`}{
        if !strings.Contains(got,expect){t.Fatalf("missing requirement %s: %s",expect,got)}
    }
    if strings.Contains(got,"secret_id") || strings.Contains(got,"/run/secrets"){t.Fatal("secret plan leaks implementation credentials")}
    after,err:=config.Load(root)
    if err!=nil||after.OpenBao==nil||len(after.OpenBao.Secrets)!=1{t.Fatal(err)}
}

func TestSecretPlanRequiresRepositoryCredentialsWhenDeploymentEnabled(t *testing.T){
    root:=t.TempDir()
    prepareProject(t,root,"api")
    key:=base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x",32)))
    cfg:=config.Document{Version:1,Project:"myapp",Deployment:&config.Deployment{
        Enabled:true,Host:"example.com",User:"deploy",Port:22,
        KnownHosts:"example.com ssh-ed25519 "+key,
    }}
    if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
    if err:=config.SaveIngress(root,[]config.Route{{Host:"api.example.com",Service:"api",Port:80}});err!=nil{t.Fatal(err)}
    var out bytes.Buffer
    if err:=checkSecrets([]string{"plan","--dir",root},&out);err!=nil{t.Fatal(err)}
    for _,name:=range []string{"SERVER_SSH_PRIVATE_KEY","OCI_REGISTRY_PULL_USERNAME","OCI_REGISTRY_PULL_TOKEN"}{
        if !strings.Contains(out.String(),name){t.Errorf("missing required GitHub secret %s",name)}
    }
}
