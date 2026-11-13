package deploycli

import (
    "bufio"
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func TestSecretCheckUsesManagedProxyEndpoint(t *testing.T) {
    root := t.TempDir()
    if err := os.MkdirAll(filepath.Join(root, "deploy"), 0700); err != nil { t.Fatal(err) }
    doc := `{"mount":"kv","secrets":[{"service":"api","name":"database_password","path":"database","field":"password"}]}`
    if err := os.WriteFile(filepath.Join(root, "deploy", "secrets.json"), []byte(doc), 0600); err != nil { t.Fatal(err) }
    var output bytes.Buffer
    const project = "bluegreenclitest001"
    err := checkSecrets([]string{"check", "--dir", root, "--project", project}, &output)
    if err == nil || !strings.Contains(err.Error(), "access OpenBao Proxy socket") {
        t.Fatalf("expected unprovisioned proxy socket error, got %v", err)
    }
    if output.Len() != 0 { t.Fatalf("unexpected output: %s", output.String()) }
}

func TestSecretCheckRejectsSocketFlag(t *testing.T) {
    err := checkSecrets([]string{"check", "--project", "myapp", "--socket", "/tmp/proxy.sock"}, &bytes.Buffer{})
    if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
        t.Fatalf("socket override should be rejected: %v", err)
    }
}

func TestSecretAddCreatesAndExtendsManifest(t *testing.T) {
    root:=t.TempDir()
    if err:=os.MkdirAll(filepath.Join(root,"deploy"),0700);err!=nil {t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"deploy","compose.yml"),[]byte(`services: {}`),0644);err!=nil{t.Fatal(err)}
    args:=[]string{"add","--dir",root,"--project","myapp","--mount","kv","--service","api","--name","database_password","--path","database","--field","password"}
    var out bytes.Buffer
    if err:=checkSecrets(args,&out);err!=nil{t.Fatal(err)}
    if err:=checkSecrets(args,&out);err!=nil{t.Fatal(err)}
    target:=filepath.Join(root,"deploy","secrets.json")
    manifest,err:=openbao.Load(target,"myapp")
    if err!=nil || len(manifest.Secrets)!=1 {t.Fatalf("manifest after repeated add: %+v %v",manifest,err)}
    second:=[]string{"add","--dir",root,"--project","myapp","--service","api","--name","api_token","--path","authentication","--field","token"}
    if err:=checkSecrets(second,&out);err!=nil{t.Fatal(err)}
    manifest,err=openbao.Load(target,"myapp")
    if err!=nil || len(manifest.Secrets)!=2 {t.Fatalf("extended manifest: %+v %v",manifest,err)}
    if err:=checkSecrets([]string{"add","--dir",root,"--project","myapp","--service","api","--name","api_token","--path","different","--field","token"},&out);err==nil{t.Fatal("overwrote secret reference")}
    info,err:=os.Stat(target)
    if err!=nil || info.Mode().Perm()!=0600 {t.Fatalf("manifest permissions: %v %v",info,err)}
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
    var out bytes.Buffer
    err:=checkSecrets([]string{"list","--dir",t.TempDir(),"--project","myapp"},&out)
    if err==nil || !strings.Contains(err.Error(),"--mount"){t.Fatalf("KV mount was guessed: %v",err)}
    if out.Len()!=0{t.Fatal("unexpected discovery output")}
    err=checkSecrets([]string{"fields","--project","myapp","--mount","kv","--path","../elsewhere"},&out)
    if err==nil{t.Fatal("accepted unsafe KV reference")}
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
