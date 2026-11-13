package openbao

import (
    "errors"
    "fmt"
    "io"
    "net/url"
    "os"
    "os/exec"
    "os/user"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
)

// ProxyProvision contains only infrastructure settings. AppRole credentials are
// read from existing administrator-provisioned files, never passed as CLI flags.
type ProxyProvision struct {
    Project string
    ServerURL string
    DeployUser string
    RoleIDFile string
    SecretIDFile string
}

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)

func ValidateProvision(p ProxyProvision) error {
    if !projectName.MatchString(p.Project) { return errors.New("invalid project name") }
    u, err := url.Parse(p.ServerURL)
    if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
        return errors.New("OpenBao server must be an HTTPS origin without credentials or path")
    }
    if p.DeployUser == "" || strings.ContainsAny(p.DeployUser, "/: \t\n") { return errors.New("deployment user is required") }
    if p.RoleIDFile == "" || p.SecretIDFile == "" || p.RoleIDFile == p.SecretIDFile {
        return errors.New("existing AppRole role ID and secret ID files are required")
    }
    return nil
}

func readIdentity(path string) ([]byte, error) {
    info, err := os.Lstat(path)
    if err != nil { return nil, fmt.Errorf("read AppRole credential: %w", err) }
    if !info.Mode().IsRegular() || info.Mode().Perm() & 0007 != 0 {
        return nil, errors.New("AppRole credential must be a regular file and must not be world-accessible")
    }
    f, err := os.Open(path)
    if err != nil { return nil, err }
    defer f.Close()
    b, err := io.ReadAll(io.LimitReader(f, 256))
    if err != nil { return nil, err }
    b = []byte(strings.TrimSpace(string(b)))
    if !identityPattern.Match(b) { return nil, errors.New("invalid AppRole identifier file") }
    return b, nil
}

// RenderProxy renders one isolated systemd-managed proxy configuration.
// The proxy uses its own AppRole identity; application containers never see it.
func RenderProxy(p ProxyProvision, executable string) (string, string, error) {
    if err := ValidateProvision(p); err != nil { return "", "", err }
    if !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\n\r") { return "", "", errors.New("invalid OpenBao executable path") }
    configDir := "/etc/bluegreen/openbao/" + p.Project
    socket, _ := ProxySocketPath(p.Project)
    cfg := fmt.Sprintf(`vault {
  address = %q
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
`, p.ServerURL, configDir+"/role_id", configDir+"/secret_id", socket)
    unit := fmt.Sprintf(`[Unit]
Description=OpenBao Proxy for Blue-Green project %s
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=%s
ExecStart=%s proxy -config=%s/proxy.hcl
Restart=on-failure
RestartSec=5
RuntimeDirectory=bluegreen/%s
RuntimeDirectoryMode=0700
UMask=0077
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`, p.Project, p.DeployUser, executable, configDir, p.Project)
    return cfg, unit, nil
}

// RenderReconciler checks only the release currently active on this host.
func RenderReconciler(p ProxyProvision, home string) (string, string, error) {
    if err := ValidateProvision(p); err != nil { return "", "", err }
    if !filepath.IsAbs(home) || !regexp.MustCompile(`^[A-Za-z0-9/_.-]+$`).MatchString(home) {
        return "", "", errors.New("unsupported deployment account home directory")
    }
    root := filepath.Join(home, ".local", "share", "docker-blue-green", p.Project)
    service := fmt.Sprintf(`[Unit]
Description=Reconcile OpenBao secrets for %s
Requires=bluegreen-openbao-%s.service
After=bluegreen-openbao-%s.service docker.service
Wants=docker.service
ConditionPathExists=%s/.bluegreen/state.json
ConditionPathExists=%s/deploy/secrets.json
ConditionPathExists=%s/.env
ConditionPathExists=%s/bluegreen

[Service]
Type=oneshot
User=%s
ExecStart=%s/bluegreen secrets reconcile --project %s --dir %s
NoNewPrivileges=yes
`, p.Project, p.Project, p.Project, root, root, root, root, p.DeployUser, root, p.Project, root)
    timer := fmt.Sprintf(`[Unit]
Description=Check OpenBao secret versions for %s

[Timer]
OnCalendar=*:0/5
Unit=bluegreen-secrets-%s.service
Persistent=false

[Install]
WantedBy=timers.target
`, p.Project, p.Project)
    return service, timer, nil
}

// ProvisionProxy sets up a project-specific Linux systemd unit. The OpenBao
// server, AppRole and its strictly scoped policy must exist beforehand.
func ProvisionProxy(p ProxyProvision) error {
    if err := ValidateProvision(p); err != nil { return err }
    if os.Geteuid() != 0 { return errors.New("proxy setup requires root privileges (sudo)") }
    if _, err := os.Stat("/run/systemd/system"); err != nil { return errors.New("systemd is required to manage OpenBao Proxy") }
    executable, err := exec.LookPath("bao")
    if err != nil { return errors.New("OpenBao binary (bao) is not installed") }
    executable, err = filepath.Abs(executable)
    if err != nil { return err }
    account, err := user.Lookup(p.DeployUser)
    if err != nil { return fmt.Errorf("deployment user does not exist: %w", err) }
    gid, err := strconv.Atoi(account.Gid)
    if err != nil { return err }
    reconcileUnit, reconcileTimer, err := RenderReconciler(p, account.HomeDir)
    if err != nil { return err }
    role, err := readIdentity(p.RoleIDFile)
    if err != nil { return err }
    secret, err := readIdentity(p.SecretIDFile)
    if err != nil { return err }
    cfg, unit, err := RenderProxy(p, executable)
    if err != nil { return err }
    configDir := "/etc/bluegreen/openbao/" + p.Project
    unitFile := "/etc/systemd/system/bluegreen-openbao-" + p.Project + ".service"
    reconcileUnitFile := "/etc/systemd/system/bluegreen-secrets-" + p.Project + ".service"
    reconcileTimerFile := "/etc/systemd/system/bluegreen-secrets-" + p.Project + ".timer"
    content := []struct{ path string; value []byte; mode os.FileMode }{
        {configDir+"/role_id", role, 0640},
        {configDir+"/secret_id", secret, 0640},
        {configDir+"/proxy.hcl", []byte(cfg), 0640},
        {unitFile, []byte(unit), 0644},
        {reconcileUnitFile, []byte(reconcileUnit), 0644},
        {reconcileTimerFile, []byte(reconcileTimer), 0644},
    }
    // All existing files must match before any write. Partial installations are
    // idempotently repairable; divergent configuration is never overwritten.
    for _, f := range content {
        b, err := os.ReadFile(f.path)
        if err == nil && string(b) != string(f.value) {
            return fmt.Errorf("existing OpenBao configuration differs: %s", f.path)
        }
        if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
    }
    // Parent directories contain no secrets. They must be traversable by
    // the unprivileged service account; the per-project directory is private.
    for _, parent := range []string{"/etc/bluegreen", "/etc/bluegreen/openbao"} {
        if err := os.MkdirAll(parent, 0755); err != nil { return err }
        if err := os.Chmod(parent, 0755); err != nil { return err }
    }
    if err := os.MkdirAll(configDir, 0750); err != nil { return err }
    if err := os.Chown(configDir, 0, gid); err != nil { return err }
    if err := os.Chmod(configDir, 0750); err != nil { return err }
    for _, f := range content {
        created, err := os.OpenFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.mode)
        if errors.Is(err, os.ErrExist) { continue }
        if err != nil { return err }
        if _, err = created.Write(f.value); err != nil { created.Close(); return err }
        if err = created.Close(); err != nil { return err }
        if err = os.Chown(f.path, 0, gid); err != nil { return err }
        if err = os.Chmod(f.path, f.mode); err != nil { return err }
    }
    for _, args := range [][]string{
        {"daemon-reload"},
        {"enable", "--now", "bluegreen-openbao-"+p.Project+".service"},
        {"enable", "--now", "bluegreen-secrets-"+p.Project+".timer"},
    } {
        cmd := exec.Command("systemctl", args...)
        output, err := cmd.CombinedOutput()
        if err != nil { return fmt.Errorf("systemctl %s: %w: %s", args[0], err, strings.TrimSpace(string(output))) }
    }
    return nil
}

