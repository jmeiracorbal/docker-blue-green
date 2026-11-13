package deploy

import (
    "context"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "io"
    "net"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

// TestRealOpenBaoBlueGreen integrates a real OpenBao server, authenticated
// OpenBao Proxy, the public CLI, Docker Compose, Caddy and both application slots.
// The disposable OpenBao dev server is strictly for CI, never for production.
func TestRealOpenBaoBlueGreen(t *testing.T) {
    if os.Getenv("BLUEGREEN_REAL_OPENBAO") != "1" {
        t.Skip("real OpenBao integration is opt-in")
    }
    ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
    defer cancel()
    const project = "bgopenbaoit"
    const devToken = "ci-disposable-openbao-root"
    const firstSecret = "demo-first-version"
    const secondSecret = "demo-second-version"
    const image = "ghcr.io/openbao/openbao:2.7.1"

    command := func(env []string, name string, args ...string) (string, error) {
        cmd := exec.CommandContext(ctx, name, args...)
        cmd.Env = append(os.Environ(), env...)
        output, err := cmd.CombinedOutput()
        if err != nil {
            return "", fmt.Errorf("command %s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
        }
        return strings.TrimSpace(string(output)), nil
    }
    mustRun := func(env []string, name string, args ...string) string {
        t.Helper()
        output, err := command(env, name, args...)
        if err != nil { t.Fatal(err) }
        return output
    }
    mustRun(nil, "docker", "info")
    for _, img := range []string{image, "nginx:1.27-alpine", "caddy:2.10-alpine"} {
        mustRun(nil, "docker", "pull", img)
    }
    serverName := "bg-openbao-"+project
    mustRun(nil, "docker", "run", "--detach", "--rm", "--name", serverName,
        "--network", "host", "-e", "BAO_DEV_ROOT_TOKEN_ID="+devToken,
        image, "server", "-dev", "-dev-no-store-token",
        "-dev-listen-address=127.0.0.1:8200")
    t.Cleanup(func() {
        stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer stopCancel()
        stop := exec.CommandContext(stopCtx, "docker", "stop", serverName)
        _,_ = stop.CombinedOutput()
    })

    temporary := t.TempDir()
    bao := filepath.Join(temporary, "bao")
    mustRun(nil, "docker", "cp", serverName+":/usr/bin/bao", bao)
    if err := os.Chmod(bao, 0700); err != nil { t.Fatal(err) }
    adminEnv := []string{"BAO_ADDR=http://127.0.0.1:8200", "BAO_TOKEN="+devToken}
    available := false
    for attempt:=0; attempt<60; attempt++ {
        if _,err:=command(adminEnv, bao, "status");err==nil { available=true;break }
        time.Sleep(150*time.Millisecond)
    }
    if !available { t.Fatal("OpenBao development server did not become ready") }

    mustRun(adminEnv, bao, "secrets", "enable", "-path=kv", "-version=2", "kv")
    mustRun(adminEnv, bao, "kv", "put", "-mount=kv", "projects/"+project+"/database", "password="+firstSecret)
    mustRun(adminEnv, bao, "kv", "put", "-mount=kv", "projects/foreign/database", "password=not-visible")
    mustRun(adminEnv, bao, "auth", "enable", "approle")

    policyFile := filepath.Join(temporary,"policy.hcl")
    policy := fmt.Sprintf(`path "kv/data/projects/%s/*" {
  capabilities = ["read"]
}
path "kv/metadata/projects/%s" {
  capabilities = ["list"]
}
path "kv/metadata/projects/%s/*" {
  capabilities = ["list"]
}
`,project,project,project)
    if err:=os.WriteFile(policyFile,[]byte(policy),0600);err!=nil{t.Fatal(err)}
    mustRun(adminEnv,bao,"policy","write",project,policyFile)
    mustRun(adminEnv,bao,"write","auth/approle/role/"+project,"token_policies="+project)
    roleID := mustRun(adminEnv,bao,"read","-field=role_id","auth/approle/role/"+project+"/role-id")
    secretID := mustRun(adminEnv,bao,"write","-f","-field=secret_id","auth/approle/role/"+project+"/secret-id")
    roleFile:=filepath.Join(temporary,"role_id")
    secretFile:=filepath.Join(temporary,"secret_id")
    if err:=os.WriteFile(roleFile,[]byte(roleID),0600);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(secretFile,[]byte(secretID),0600);err!=nil{t.Fatal(err)}

    base := "/run/bluegreen"
    socketDir:=filepath.Join(base,project)
    if os.Geteuid()==0 {
        if err:=os.MkdirAll(socketDir,0700);err!=nil{t.Fatal(err)}
    } else {
        mustRun(nil,"sudo","install","-d","-m","0755",base)
        mustRun(nil,"sudo","install","-d","-m","0700",
            "-o",strconv.Itoa(os.Getuid()),"-g",strconv.Itoa(os.Getgid()),socketDir)
    }
    t.Cleanup(func(){ _=os.RemoveAll(socketDir); _=os.Remove(base) })
    socket,err:=openbao.ProxySocketPath(project)
    if err!=nil{t.Fatal(err)}
    proxyConfig:=fmt.Sprintf(`vault {
  address = "http://127.0.0.1:8200"
}
auto_auth {
  method "approle" {
    config = {
      role_id_file_path = %q
      secret_id_file_path = %q
      remove_secret_id_file_after_reading = false
    }
  }
}
api_proxy {
  use_auto_auth_token = "force"
}
listener "unix" {
  address = %q
  socket_mode = "0600"
  tls_disable = true
}
`,roleFile,secretFile,socket)
    configFile:=filepath.Join(temporary,"proxy.hcl")
    if err:=os.WriteFile(configFile,[]byte(proxyConfig),0600);err!=nil{t.Fatal(err)}
    proxy:=exec.CommandContext(ctx,bao,"proxy","-config="+configFile)
    proxy.Stdout=io.Discard
    proxy.Stderr=io.Discard
    if err:=proxy.Start();err!=nil{t.Fatal(err)}
    t.Cleanup(func(){
        _=proxy.Process.Kill()
        _=proxy.Wait()
    })
    var client *openbao.Client
    var keys []string
    ready:=false
    for attempt:=0;attempt<80;attempt++{
        client,err=openbao.NewClient(project)
        if err==nil{
            keys,err=client.ListKeys(ctx,project,"kv")
            if err==nil {ready=true;break}
        }
        time.Sleep(150*time.Millisecond)
    }
    if !ready {t.Fatalf("authenticated OpenBao Proxy unavailable: %v",err)}
    if len(keys)!=1 || keys[0]!="database" {t.Fatalf("project KV isolation failed: %v",keys)}
    forbiddenClient:=&http.Client{Transport:&http.Transport{DialContext:func(ctx context.Context,_,_ string)(net.Conn,error){
        return (&net.Dialer{}).DialContext(ctx,"unix",socket)
    }}}
    forbidden,err:=forbiddenClient.Get("http://openbao/v1/kv/data/projects/foreign/database")
    if err!=nil{t.Fatal(err)}
    forbidden.Body.Close()
    if forbidden.StatusCode!=http.StatusForbidden {t.Fatalf("cross-project KV read was not denied: HTTP %d",forbidden.StatusCode)}

    projectRoot:=t.TempDir()
    if err:=os.CopyFS(projectRoot,os.DirFS("../../examples/openbao-demo"));err!=nil{t.Fatal(err)}
    cli:=filepath.Join(temporary,"bluegreen")
    mustRun(nil,"go","build","-o",cli,"../../cmd/bluegreen")
    invokeCLI:=func(stdin string,args ...string)string{
        t.Helper()
        cmd:=exec.CommandContext(ctx,cli,args...)
        cmd.Dir=projectRoot
        cmd.Stdin=strings.NewReader(stdin)
        output,err:=cmd.CombinedOutput()
        if err!=nil{t.Fatalf("bluegreen command failed: %v: %s",err,strings.TrimSpace(string(output)))}
        return string(output)
    }
    listed:=invokeCLI("","secrets","list","--project",project,"--mount","kv","--dir",projectRoot)
    if strings.TrimSpace(listed)!="database" {t.Fatalf("unexpected KV paths: %q",listed)}
    fields:=invokeCLI("","secrets","fields","--project",project,"--mount","kv","--path","database","--dir",projectRoot)
    if strings.TrimSpace(fields)!="password" {t.Fatalf("unexpected KV fields: %q",fields)}
    selected:=invokeCLI("1\n1\n","secrets","add","--project",project,"--mount","kv",
        "--service","app","--name","database_password","--dir",projectRoot)
    if !strings.Contains(selected,"OpenBao secret reference configured") {t.Fatalf("KV association failed: %s",selected)}
    checked:=invokeCLI("","secrets","check","--project",project,"--dir",projectRoot)
    if !strings.Contains(checked,"1 OpenBao secret references verified"){t.Fatalf("KV reference verification failed: %s",checked)}

    applicationDigest:=mustRun(nil,"docker","image","inspect","--format","{{index .RepoDigests 0}}","nginx:1.27-alpine")
    httpListener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
    httpPort:=httpListener.Addr().(*net.TCPAddr).Port;httpListener.Close()
    httpsListener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
    httpsPort:=httpsListener.Addr().(*net.TCPAddr).Port;httpsListener.Close()
    envFile:=filepath.Join(projectRoot,".env")
    envText:=fmt.Sprintf("DEPLOY_NETWORK=%s-edge\nCADDY_IMAGE=caddy:2.10-alpine\nCADDY_ACME_EMAIL=test@example.invalid\nHTTP_PORT=%d\nHTTPS_PORT=%d\n",
        project,httpPort,httpsPort)
    if err:=os.WriteFile(envFile,[]byte(envText),0600);err!=nil{t.Fatal(err)}
    t.Setenv("DEPLOY_NETWORK",project+"-edge")
    t.Setenv("CADDY_IMAGE","caddy:2.10-alpine")
    t.Setenv("CADDY_ACME_EMAIL","test@example.invalid")
    t.Setenv("HTTP_PORT",strconv.Itoa(httpPort))
    t.Setenv("HTTPS_PORT",strconv.Itoa(httpsPort))
    release:=Release{Project:project,Version:"v1",Images:map[string]string{"app":applicationDigest},
        Routes:[]Route{{Host:"app.localhost",Service:"app",Port:80}}}
    releaseFile:=filepath.Join(projectRoot,"release.json")
    encoded,err:=json.Marshal(release)
    if err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(releaseFile,encoded,0600);err!=nil{t.Fatal(err)}

    cleanupController:=Controller{Root:projectRoot,EnvFile:envFile}
    t.Cleanup(func(){
        for _,slot:=range []string{"blue","green"}{
            env:=cleanupController.environment(release,slot)
            _=ExecRunner{}.Run(context.Background(),projectRoot,env,"docker","compose","--env-file",envFile,
                "-f",filepath.Join(projectRoot,"deploy","compose.yml"),"down","--remove-orphans")
        }
        _=ExecRunner{}.Run(context.Background(),projectRoot,cleanupController.environment(release,"blue"),"docker",
            "compose","--env-file",envFile,"-f",filepath.Join(projectRoot,"deploy","infrastructure","compose.yml"),
            "down","--volumes","--remove-orphans")
    })
    probe:=func(){
        t.Helper()
        transport:=&http.Transport{TLSClientConfig:&tls.Config{InsecureSkipVerify:true}}
        defer transport.CloseIdleConnections()
        httpClient:=&http.Client{Transport:transport,Timeout:3*time.Second}
        address:=fmt.Sprintf("https://127.0.0.1:%d/",httpsPort)
        deadline:=time.Now().Add(30*time.Second)
        for time.Now().Before(deadline){
            request,_:=http.NewRequestWithContext(ctx,http.MethodGet,address,nil)
            request.Host="app.localhost"
            response,err:=httpClient.Do(request)
            if err==nil{
                response.Body.Close()
                if response.StatusCode==http.StatusOK{return}
            }
            time.Sleep(400*time.Millisecond)
        }
        t.Fatalf("Caddy could not route to the active application")
    }
    slotSecret:=func(slot string)string {
        t.Helper()
        container:=mustRun(nil,"docker","ps","-q",
            "--filter","label=com.docker.compose.project="+project+"-"+slot,
            "--filter","label=com.docker.compose.service=app")
        if container==""{t.Fatalf("missing %s application container",slot)}
        return mustRun(nil,"docker","exec",container,"cat","/run/secrets/database_password")
    }

    invokeCLI("","deploy","--dir",projectRoot,"--release",releaseFile,"--env-file",envFile)
    probe()
    if actual:=slotSecret("blue");actual!=firstSecret{t.Fatalf("blue did not mount initial KV secret")}
    mustRun(adminEnv,bao,"kv","put","-mount=kv","projects/"+project+"/database","password="+secondSecret)
    invokeCLI("","secrets","reconcile","--project",project,"--dir",projectRoot)
    probe()
    if actual:=slotSecret("green");actual!=secondSecret{t.Fatalf("green did not receive rotated KV secret")}
    invokeCLI("","rollback","--project",project,"--dir",projectRoot,"--env-file",envFile)
    probe()
    if actual:=slotSecret("blue");actual!=firstSecret{t.Fatalf("rollback did not preserve old secret")}
    invokeCLI("","secrets","reconcile","--project",project,"--dir",projectRoot)
    if actual:=slotSecret("blue");actual!=firstSecret{t.Fatalf("reconciliation undid explicit rollback")}
}
