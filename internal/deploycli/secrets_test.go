package deploycli

import (
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
