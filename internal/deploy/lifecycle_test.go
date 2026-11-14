package deploy

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func lifecycleFixture(t *testing.T, root string) {
    t.Helper()
    cfg,err:=config.Load(root)
    if err!=nil{t.Fatal(err)}
    cfg.Lifecycle=&config.Lifecycle{
        BeforeStart:[]config.LifecycleStep{{
            Service:"api",Entrypoint:"python",Args:[]string{"manage.py","migrate","--noinput"},TimeoutSeconds:120,
        }},
        BeforeSwitch:[]config.LifecycleStep{{
            Service:"api",Entrypoint:"/app/verify",Args:[]string{"--readiness"},TimeoutSeconds:90,
        }},
    }
    if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
    content:="services:\n  api:\n    image: ${API_IMAGE}\n"
    if err:=os.WriteFile(filepath.Join(root,"deploy","compose.yml"),[]byte(content),0644);err!=nil{t.Fatal(err)}
}

func callIndex(calls []string, fragment string) int {
    for i,call:=range calls {if strings.Contains(call,fragment){return i}}
    return -1
}

func TestLifecycleRunsBeforeStartAndBeforeCutover(t *testing.T) {
    root:=testRoot(t)
    lifecycleFixture(t,root)
    runner:=&fakeRunner{}
    controller:=Controller{Root:root,Runner:runner}
    if err:=controller.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
    migrate:=callIndex(runner.calls,"--entrypoint python api manage.py migrate --noinput")
    up:=callIndex(runner.calls,"up -d --no-build")
    health:=callIndex(runner.calls,"docker inspect --format")
    guard:=callIndex(runner.calls,"--entrypoint /app/verify api --readiness")
    switchTraffic:=callIndex(runner.calls,"caddy reload")
    if migrate<0 || up<0 || health<0 || guard<0 || switchTraffic<0 ||
        !(migrate<up && up<health && health<guard && guard<switchTraffic) {
        t.Fatalf("incorrect lifecycle order: %v",runner.calls)
    }
    if err:=controller.Rollback(context.Background(),"demo");err==nil {
        t.Fatal("initial release unexpectedly has a rollback target")
    }
}

func TestMigrationFailureDoesNotSwitchTraffic(t *testing.T) {
    root:=testRoot(t)
    lifecycleFixture(t,root)
    runner:=&fakeRunner{}
    c:=Controller{Root:root,Runner:runner}
    if err:=c.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
    runner.failOn="--entrypoint python api"
    if err:=c.Deploy(context.Background(),release("v2"));err==nil || !strings.Contains(err.Error(),"before_start") {
        t.Fatalf("expected migration failure: %v",err)
    }
    route,err:=os.ReadFile(routeFile(root,"demo"));if err!=nil{t.Fatal(err)}
    if !strings.Contains(string(route),"demo-blue-api"){t.Fatal("migration failure switched active traffic")}
    state,err:=readState(filepath.Join(root,".bluegreen"));if err!=nil{t.Fatal(err)}
    if state.ActiveVersion!="v1" || state.Phase!="preparing" {t.Fatalf("unexpected state: %+v",state)}
    runner.failOn=""
    if err:=c.Deploy(context.Background(),release("v2"));err!=nil{t.Fatal(err)}
}

func TestValidationFailureBlocksSwitchAndAllowsRetry(t *testing.T) {
    root:=testRoot(t)
    lifecycleFixture(t,root)
    runner:=&fakeRunner{}
    c:=Controller{Root:root,Runner:runner}
    if err:=c.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
    runner.failOn="--entrypoint /app/verify api --readiness"
    if err:=c.Deploy(context.Background(),release("v2"));err==nil || !strings.Contains(err.Error(),"before_switch") {
        t.Fatalf("expected validation failure: %v",err)
    }
    route,err:=os.ReadFile(routeFile(root,"demo"));if err!=nil{t.Fatal(err)}
    if !strings.Contains(string(route),"demo-blue-api"){t.Fatal("validation failure switched active traffic")}
    runner.failOn=""
    if err:=c.Deploy(context.Background(),release("v2"));err!=nil{t.Fatal(err)}
    state,err:=readState(filepath.Join(root,".bluegreen"));if err!=nil{t.Fatal(err)}
    if state.ActiveVersion!="v2" || state.ActiveSlot!="green"{t.Fatalf("retry failed: %+v",state)}
    if err:=c.Rollback(context.Background(),"demo");err!=nil{t.Fatal(err)}
    state,err=readState(filepath.Join(root,".bluegreen"));if err!=nil{t.Fatal(err)}
    if state.ActiveVersion!="v1" || state.ActiveLifecycleHash=="" {t.Fatalf("rollback lost lifecycle reference: %+v",state)}
}

func TestLifecycleChangeRequiresNewRelease(t *testing.T) {
    root:=testRoot(t)
    lifecycleFixture(t,root)
    runner:=&fakeRunner{}
    c:=Controller{Root:root,Runner:runner}
    if err:=c.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
    cfg,err:=config.Load(root);if err!=nil{t.Fatal(err)}
    cfg.Lifecycle.BeforeStart[0].Args=append(cfg.Lifecycle.BeforeStart[0].Args,"--changed")
    if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
    count:=len(runner.calls)
    if err:=c.Deploy(context.Background(),release("v1"));err==nil || !strings.Contains(err.Error(),"lifecycle changed"){
        t.Fatalf("accepted altered migration with same release: %v",err)
    }
    if len(runner.calls)!=count{t.Fatal("Docker ran after refusing changed lifecycle")}
}

func TestAutomaticSecretRotationSkipsLifecycleOperations(t *testing.T) {
    root:=testRoot(t)
    lifecycleFixture(t,root)
    runner:=&fakeRunner{}
    c:=Controller{Root:root,Runner:runner}
    if err:=c.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
    cfg,err:=config.Load(root);if err!=nil{t.Fatal(err)}
    ref:=openbao.Reference{Service:"api",Name:"token",Path:"auth",Field:"token"}
    cfg.OpenBao=&config.OpenBao{Mount:"kv",Secrets:[]openbao.Reference{ref}}
    if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
    state,err:=readState(filepath.Join(root,".bluegreen"));if err!=nil{t.Fatal(err)}
    runner.calls=nil
    c.Automatic=true
    c.ExpectedActiveHash=state.ActiveHash
    c.SecretResolver=func(context.Context,string,openbao.Manifest)([]openbao.Resolved,error){
        return []openbao.Resolved{{Reference:ref,Version:2,Data:[]byte("ci-test-credential")}},nil
    }
    if err:=c.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
    for _,call:=range runner.calls {
        if strings.Contains(call,"--entrypoint") {t.Fatalf("automatic rotation executed a lifecycle operation: %s",call)}
    }
}

func TestLifecycleHooksAreFrameworkNeutral(t *testing.T) {
    cases:=[]struct {
        name string
        before config.LifecycleStep
        after config.LifecycleStep
    }{
        {name:"django",before:config.LifecycleStep{Service:"api",Entrypoint:"python",Args:[]string{"manage.py","migrate","--noinput"},TimeoutSeconds:60},
          after:config.LifecycleStep{Service:"api",Entrypoint:"python",Args:[]string{"manage.py","check","--deploy"},TimeoutSeconds:30}},
        {name:"node",before:config.LifecycleStep{Service:"api",Entrypoint:"node",Args:[]string{"scripts/migrate.js"},TimeoutSeconds:60},
          after:config.LifecycleStep{Service:"api",Entrypoint:"node",Args:[]string{"scripts/validate.js"},TimeoutSeconds:30}},
        {name:"go",before:config.LifecycleStep{Service:"api",Entrypoint:"/app/migrate",TimeoutSeconds:60},
          after:config.LifecycleStep{Service:"api",Entrypoint:"/app/verify",TimeoutSeconds:30}},
    }
    for _,tc:=range cases{
        t.Run(tc.name,func(t *testing.T){
            root:=testRoot(t)
            cfg,err:=config.Load(root)
            if err!=nil{t.Fatal(err)}
            cfg.Lifecycle=&config.Lifecycle{BeforeStart:[]config.LifecycleStep{tc.before},
                BeforeSwitch:[]config.LifecycleStep{tc.after}}
            if err:=config.Save(root,cfg);err!=nil{t.Fatal(err)}
            runner:=&fakeRunner{}
            c:=Controller{Root:root,Runner:runner}
            if err:=c.Deploy(context.Background(),release("v1"));err!=nil{t.Fatal(err)}
            command:=func(step config.LifecycleStep) string {
                suffix:="--entrypoint "+step.Entrypoint+" api"
                if len(step.Args)>0 {suffix+=" "+strings.Join(step.Args," ")}
                return suffix
            }
            migrate:=callIndex(runner.calls,command(tc.before))
            verify:=callIndex(runner.calls,command(tc.after))
            traffic:=callIndex(runner.calls,"caddy reload")
            if migrate<0 || verify<0 || traffic<0 || !(migrate<verify && verify<traffic){
                t.Fatalf("incorrect %s lifecycle order: %v",tc.name,runner.calls)
            }
        })
    }
}
