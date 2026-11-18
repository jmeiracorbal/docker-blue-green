package edge

import (
 "context"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
)
type fakeRunner struct { calls []string; fail string }
func (f *fakeRunner) Run(_ context.Context,_ string,_ []string,bin string,args ...string)error {
 s:=bin+" "+strings.Join(args," ")
 f.calls=append(f.calls,s)
 if f.fail!=""&&strings.Contains(s,f.fail){return errors.New("simulated failure")}
 return nil
}
func testManager(t *testing.T)*Manager{
 t.Helper()
 root:=filepath.Join(t.TempDir(),"edge")
 t.Setenv("DEPLOY_NETWORK","shared-edge-ci")
 t.Setenv("CADDY_IMAGE","caddy:2.10-alpine")
 t.Setenv("CADDY_ACME_EMAIL","test@example.invalid")
 t.Setenv("HTTP_PORT","18080")
 t.Setenv("HTTPS_PORT","18443")
 return &Manager{Root:root,Runner:&fakeRunner{}}
}
func TestEnsureIsIdempotentAndCorrective(t *testing.T) {
 m:=testManager(t);ctx:=context.Background()
 if err:=m.Ensure(ctx);err!=nil{t.Fatal(err)}
 if err:=m.Ensure(ctx);err!=nil{t.Fatal(err)}
 b,err:=os.ReadFile(filepath.Join(m.Root,"Caddyfile"))
 if err!=nil{t.Fatal(err)}
 if !strings.Contains(string(b),"/etc/caddy/apps/*.caddy"){t.Fatal(string(b))}
 if err=os.WriteFile(filepath.Join(m.Root,"Caddyfile"),[]byte("broken"),0644);err!=nil{t.Fatal(err)}
 if err=m.Ensure(ctx);err!=nil{t.Fatal(err)}
 b,_=os.ReadFile(filepath.Join(m.Root,"Caddyfile"))
 if string(b)!=(caddyfile){t.Fatal("did not repair Edge base configuration")}
 t.Setenv("DEPLOY_NETWORK","another-network")
 if err=m.Ensure(ctx);err==nil{t.Fatal("adopted incompatible network")}
}
func TestMultiProjectIsolationAndCollision(t *testing.T) {
 m:=testManager(t);ctx:=context.Background()
 if err:=m.Ensure(ctx);err!=nil{t.Fatal(err)}
 if err:=m.Publish(ctx,"first","blue",[]Route{{Host:"api.example.com",Service:"api",Port:8080}});err!=nil{t.Fatal(err)}
 if err:=m.Publish(ctx,"second","blue",[]Route{{Host:"billing.example.com",Service:"api",Port:8080}});err!=nil{t.Fatal(err)}
 before,_:=os.ReadFile(m.RouteFile("second"))
 if err:=m.Publish(ctx,"first","green",[]Route{{Host:"api.example.com",Service:"api",Port:8080}});err!=nil{t.Fatal(err)}
 after,_:=os.ReadFile(m.RouteFile("second"))
 if string(before)!=string(after){t.Fatal("another project's route changed")}
 if err:=m.Publish(ctx,"third","blue",[]Route{{Host:"billing.example.com",Service:"api",Port:8080}});err==nil{t.Fatal("duplicate host accepted")}
 if err:=os.Remove(m.RouteFile("first"));err!=nil{t.Fatal(err)}
 if err:=m.Publish(ctx,"first","green",[]Route{{Host:"api.example.com",Service:"api",Port:8080}});err!=nil{t.Fatal(err)}
}
func TestFailedReloadRestoresOriginal(t *testing.T){
 m:=testManager(t);ctx:=context.Background()
 if err:=m.Ensure(ctx);err!=nil{t.Fatal(err)}
 r:=[]Route{{Host:"app.example.com",Service:"api",Port:80}}
 if err:=m.Publish(ctx,"app","blue",r);err!=nil{t.Fatal(err)}
 f:=m.Runner.(*fakeRunner)
 f.fail="caddy reload"
 if err:=m.Publish(ctx,"app","green",r);err==nil{t.Fatal("expected failure")}
 b,_:=os.ReadFile(m.RouteFile("app"))
 if !strings.Contains(string(b),"app-blue-api:80"){t.Fatal(string(b))}
}
