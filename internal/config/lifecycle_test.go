package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLifecycleContractAcceptsStructuredSteps(t *testing.T) {
    raw:=`version: 1
project: demo
routes:
  - host: demo.example.com
    service: api
    port: 8080
lifecycle:
  before_start:
    - service: api
      entrypoint: python
      args: [manage.py, migrate, --noinput]
      timeout_seconds: 180
  before_switch:
    - service: schema-check
      entrypoint: schema-guard
      args: [check, --engine, postgresql, --expected, /schema/expected.json]
      timeout_seconds: 60
`
    cfg,err:=Parse([]byte(raw))
    if err!=nil{t.Fatal(err)}
    if len(cfg.Lifecycle.BeforeStart)!=1 || len(cfg.Lifecycle.BeforeSwitch)!=1 {
        t.Fatalf("lifecycle not parsed: %+v",cfg.Lifecycle)
    }
}

func TestLifecycleRejectsInvalidSteps(t *testing.T) {
    base:=Document{Version:1,Project:"demo",Routes:[]Route{{Host:"demo.example.com",Service:"api",Port:8080}}}
    cases:=[]LifecycleStep{
        {Service:"bad/service",Entrypoint:"python",TimeoutSeconds:30},
        {Service:"api",Entrypoint:"/bin/sh -c",TimeoutSeconds:30},
        {Service:"api",Entrypoint:"python",TimeoutSeconds:0},
        {Service:"api",Entrypoint:"python",TimeoutSeconds:3601},
        {Service:"api",Entrypoint:"python",Args:[]string{"bad\nargument"},TimeoutSeconds:30},
    }
    for _,step:=range cases {
        base.Lifecycle=&Lifecycle{BeforeStart:[]LifecycleStep{step}}
        if err:=base.Validate(true);err==nil{t.Errorf("accepted invalid step: %+v",step)}
    }
    if _,err:=Parse([]byte("version: 1\nproject: demo\nroutes: []\nlifecycle:\n  before_start:\n    - service: api\n      entrypoint: python\n      timeout_seconds: 60\n      shell: true\n"));err==nil{
        t.Fatal("accepted unknown lifecycle field")
    }
}

func TestLifecycleValidatesComposeServicesAndReleaseImages(t *testing.T) {
    root:=t.TempDir()
    if err:=os.MkdirAll(filepath.Join(root,"deploy"),0755);err!=nil{t.Fatal(err)}
    compose:="services:\n  api:\n    image: ${API_IMAGE}\n  worker:\n    image: ${WORKER_IMAGE}\n"
    if err:=os.WriteFile(filepath.Join(root,"deploy","compose.yml"),[]byte(compose),0644);err!=nil{t.Fatal(err)}
    cfg:=Document{Version:1,Project:"demo",Routes:[]Route{{Host:"demo.example.com",Service:"api",Port:8080}},
        Lifecycle:&Lifecycle{BeforeSwitch:[]LifecycleStep{{Service:"worker",Entrypoint:"/app/validate",TimeoutSeconds:120}}},
    }
    if err:=cfg.ValidateApplicationServices(root);err!=nil{t.Fatal(err)}
    if err:=cfg.ValidateLifecycleServices(map[string]string{"api":"pinned-image"});err==nil{
        t.Fatal("accepted hook service absent from release image set")
    }
    if err:=cfg.ValidateLifecycleServices(map[string]string{"api":"pinned-image","worker":"pinned-worker"});err!=nil{
        t.Fatal(err)
    }
    cfg.Lifecycle.BeforeSwitch[0].Service="missing"
    if err:=cfg.ValidateApplicationServices(root);err==nil{t.Fatal("accepted undefined service")}
}
