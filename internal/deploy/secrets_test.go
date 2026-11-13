package deploy

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func openbaoFixture(t *testing.T, root string) {
    t.Helper()
    doc:=`{"mount":"kv","secrets":[{"service":"api","name":"database_password","path":"db","field":"password"}]}`
    if err:=os.WriteFile(filepath.Join(root,"deploy","secrets.json"),[]byte(doc),0600);err!=nil {t.Fatal(err)}
}

func secretResolver(version int) SecretResolver {
    return func(_ context.Context, _ string, m openbao.Manifest)([]openbao.Resolved,error) {
        return []openbao.Resolved{{
            Reference:m.Secrets[0],Version:version,Data:[]byte("private-example"),
        }},nil
    }
}

func TestDeployWithOpenBaoSecretsAndRollback(t *testing.T) {
    root:=testRoot(t)
    openbaoFixture(t,root)
    runner:=&fakeRunner{}
    c:=Controller{Root:root,Runner:runner,SecretResolver:secretResolver(1)}
    ctx:=context.Background()
    r:=release("v1")
    if err:=c.Deploy(ctx,r);err!=nil {t.Fatal(err)}
    first,err:=readState(filepath.Join(root,".bluegreen"))
    if err!=nil {t.Fatal(err)}
    if first.ActiveSecretRevision=="" || first.ActiveSecretOverlay=="" {t.Fatalf("no active snapshot: %+v",first)}
    var overlay struct{
        Services map[string]struct{Secrets []struct{Source string `json:"source"`;Target string `json:"target"`} `json:"secrets"`} `json:"services"`
        Secrets map[string]struct{File string `json:"file"`} `json:"secrets"`
    }
    b,err:=os.ReadFile(first.ActiveSecretOverlay)
    if err!=nil {t.Fatal(err)}
    if err:=json.Unmarshal(b,&overlay);err!=nil{t.Fatal(err)}
    if !strings.Contains(string(b), "api_database_password") || strings.Contains(string(b),"private-example") {t.Fatal("unsafe overlay")}
    if overlay.Services["api"].Secrets[0].Target!="database_password" {t.Fatal("wrong container secret target")}
    secretPath:=overlay.Secrets["api_database_password"].File
    val,err:=os.ReadFile(secretPath)
    if err!=nil || string(val)!="private-example" {t.Fatalf("secret snapshot error: %v",err)}
    if info,err:=os.Stat(filepath.Dir(secretPath));err!=nil || info.Mode().Perm()!=0700 {t.Fatalf("snapshot directory not private: %v",err)}
    if info,err:=os.Stat(secretPath);err!=nil || info.Mode().Perm()!=0444 {t.Fatalf("secret file mode wrong: %v",err)}
    if len(runner.calls)==0 || !strings.Contains(strings.Join(runner.calls, `
`),first.ActiveSecretOverlay) {t.Fatal("Compose did not use OpenBao overlay")}
    count:=len(runner.calls)
    if err:=c.Deploy(ctx,r);err!=nil {t.Fatal(err)}
    if len(runner.calls)!=count {t.Fatal("redeployed unmodified secret version")}
    c.SecretResolver=secretResolver(2)
    if err:=c.Deploy(ctx,r);err!=nil {t.Fatal(err)}
    next,err:=readState(filepath.Join(root,".bluegreen"))
    if err!=nil{t.Fatal(err)}
    if next.ActiveSlot!="green" || next.PreviousSlot!="blue" || next.ActiveHash==first.ActiveHash || next.PreviousSecretOverlay!=first.ActiveSecretOverlay {
        t.Fatalf("rotation failed: %+v",next)
    }
    if err:=c.Rollback(ctx,"demo");err!=nil{t.Fatal(err)}
    restored,err:=readState(filepath.Join(root,".bluegreen"))
    if err!=nil{t.Fatal(err)}
    if restored.ActiveSecretOverlay!=first.ActiveSecretOverlay {t.Fatal("rollback lost previous secret snapshot")}
}

func TestFailedOpenBaoReadDoesNotStartDocker(t *testing.T) {
    root:=testRoot(t)
    openbaoFixture(t,root)
    runner:=&fakeRunner{}
    c:=Controller{Root:root,Runner:runner,SecretResolver:func(_ context.Context,_ string,_ openbao.Manifest)([]openbao.Resolved,error){
        return nil,os.ErrPermission
    }}
    if err:=c.Deploy(context.Background(),release("v1"));err==nil || !strings.Contains(err.Error(),"OpenBao"){t.Fatalf("expected OpenBao failure: %v",err)}
    if len(runner.calls)!=0 {t.Fatalf("Docker started on missing secrets: %v",runner.calls)}
}

func TestSnapshotRejectsInconsistentExistingContent(t *testing.T) {
    root:=testRoot(t)
    snapshot:=&secretSnapshot{values:[]openbao.Resolved{{Reference:openbao.Reference{Service:"api",Name:"password"},Data:[]byte("first"),Version:1}},revision:strings.Repeat("a",64)}
    if _,err:=stageSecretOverlay(root,"blue",snapshot);err!=nil{t.Fatal(err)}
    snapshot.values[0].Data=[]byte("second")
    if _,err:=stageSecretOverlay(root,"blue",snapshot);err==nil {t.Fatal("accepted changed data under same KV revision")}
}

func TestAutomaticReconciliationPreservesRollbackIntent(t *testing.T) {
    root:=testRoot(t)
    openbaoFixture(t,root)
    runner:=&fakeRunner{}
    currentVersion:=1
    c:=Controller{Root:root,Runner:runner}
    c.SecretResolver=func(_ context.Context,_ string,m openbao.Manifest)([]openbao.Resolved,error) {
        return []openbao.Resolved{{Reference:m.Secrets[0],Version:currentVersion,Data:[]byte("rotatable-value")}},nil
    }
    ctx:=context.Background()
    r:=release("v1")
    if err:=c.Deploy(ctx,r);err!=nil{t.Fatal(err)}
    currentVersion=2
    before:=len(runner.calls)
    if err:=c.ReconcileSecrets(ctx,"demo");err!=nil{t.Fatal(err)}
    state,err:=readState(filepath.Join(root,".bluegreen"))
    if err!=nil{t.Fatal(err)}
    if state.ActiveSlot!="green" || state.ActiveVersion!="v1" {t.Fatalf("reconciler changed release unexpectedly: %+v",state)}
    for _,call:=range runner.calls[before:]{
        if strings.Contains(call," pull") {t.Fatalf("registry pull on secret-only rotation: %s",call)}
    }
    if err:=c.Rollback(ctx,"demo");err!=nil{t.Fatal(err)}
    state,err=readState(filepath.Join(root,".bluegreen"))
    if err!=nil{t.Fatal(err)}
    if !state.AutoReconcilePaused {t.Fatal("automatic reconciliation was not paused after rollback")}
    callCount:=len(runner.calls)
    if err:=c.ReconcileSecrets(ctx,"demo");err!=nil{t.Fatal(err)}
    if len(runner.calls)!=callCount{t.Fatal("timer undid manual rollback")}
    if err:=c.Deploy(ctx,r);err!=nil{t.Fatal(err)}
    state,err=readState(filepath.Join(root,".bluegreen"))
    if err!=nil{t.Fatal(err)}
    if state.AutoReconcilePaused {t.Fatal("manual deployment did not resume reconciliation")}
}

func TestReconcileRejectsUninitializedProject(t *testing.T) {
    root:=testRoot(t)
    c:=Controller{Root:root,Runner:&fakeRunner{}}
    if err:=c.ReconcileSecrets(context.Background(),"demo");err==nil {
        t.Fatal("accepted reconciliation without initial deployment")
    }
}
