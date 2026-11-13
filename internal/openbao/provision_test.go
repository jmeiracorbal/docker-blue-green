package openbao

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestRenderProjectProxy(t *testing.T) {
    p := ProxyProvision{
        Project: "project-a", ServerURL: "https://openbao.internal:8200",
        DeployUser: "deploy-a", RoleIDFile: "/secure/role", SecretIDFile: "/secure/secret",
    }
    cfg, unit, err := RenderProxy(p, "/usr/local/bin/bao")
    if err != nil { t.Fatal(err) }
    for _, want := range []string{
        `address = "/run/bluegreen/project-a/openbao.sock"`,
        `address = "https://openbao.internal:8200"`,
        `use_auto_auth_token = "force"`,
        "remove_secret_id_file_after_reading = false",
        "socket_mode = "0600"",
    } {
        if !strings.Contains(cfg, want) { t.Errorf("config is missing %q", want) }
    }
    for _, want := range []string{"User=deploy-a", "RuntimeDirectory=bluegreen/project-a", "UMask=0077", "NoNewPrivileges=yes"} {
        if !strings.Contains(unit, want) { t.Errorf("unit is missing %q", want) }
    }
    if strings.Contains(cfg, "secure/role") || strings.Contains(cfg, "secure/secret") {
        t.Error("source identity file paths leaked into provisioned config")
    }
}

func TestRejectUnsafeProxySetup(t *testing.T) {
    valid := ProxyProvision{
        Project:"project-a", ServerURL:"https://bao.internal",
        DeployUser:"deploy", RoleIDFile:"/tmp/role", SecretIDFile:"/tmp/secret",
    }
    tests := []ProxyProvision{
        {Project:"../escape", ServerURL:valid.ServerURL, DeployUser:valid.DeployUser, RoleIDFile:valid.RoleIDFile, SecretIDFile:valid.SecretIDFile},
        {Project:valid.Project, ServerURL:"http://bao.internal", DeployUser:valid.DeployUser, RoleIDFile:valid.RoleIDFile, SecretIDFile:valid.SecretIDFile},
        {Project:valid.Project, ServerURL:"https://user:password@bao.internal", DeployUser:valid.DeployUser, RoleIDFile:valid.RoleIDFile, SecretIDFile:valid.SecretIDFile},
        {Project:valid.Project, ServerURL:valid.ServerURL, DeployUser:"deploy;rm -rf /", RoleIDFile:valid.RoleIDFile, SecretIDFile:valid.SecretIDFile},
        {Project:valid.Project, ServerURL:valid.ServerURL, DeployUser:valid.DeployUser, RoleIDFile:valid.RoleIDFile, SecretIDFile:valid.RoleIDFile},
    }
    for _, p := range tests {
        if err := ValidateProvision(p); err == nil { t.Errorf("unsafe proxy configuration allowed: %+v", p) }
    }
    if _, _, err := RenderProxy(valid, "relative/bao"); err == nil { t.Fatal("accepted relative executable") }
}

func TestAppRoleFileMustBePrivate(t *testing.T) {
    file := filepath.Join(t.TempDir(), "secret_id")
    if err := os.WriteFile(file, []byte("abcdefgh-12345678"), 0644); err != nil { t.Fatal(err) }
    if _, err := readIdentity(file); err == nil { t.Fatal("world-readable AppRole accepted") }
    if err := os.Chmod(file, 0600); err != nil { t.Fatal(err) }
    b, err := readIdentity(file)
    if err != nil || string(b) != "abcdefgh-12345678" { t.Fatalf("invalid credential: %v", err) }
}
