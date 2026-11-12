package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Route struct {
	Host    string `json:"host"`
	Service string `json:"service"`
	Port    int    `json:"port"`
}

type Release struct {
	Project string            `json:"project"`
	Version string            `json:"version"`
	Images  map[string]string `json:"images"`
	Routes  []Route           `json:"routes"`
}

type State struct {
	ActiveSlot      string            `json:"active_slot,omitempty"`
	ActiveVersion   string            `json:"active_version,omitempty"`
	ActiveHash      string            `json:"active_hash,omitempty"`
	ActiveRoutes    []Route           `json:"active_routes,omitempty"`
	PreviousSlot    string            `json:"previous_slot,omitempty"`
	PreviousVersion string            `json:"previous_version,omitempty"`
	PreviousHash    string            `json:"previous_hash,omitempty"`
	PreviousRoutes  []Route           `json:"previous_routes,omitempty"`
	ActiveImages    map[string]string `json:"active_images,omitempty"`
	PreviousImages  map[string]string `json:"previous_images,omitempty"`
	Phase           string            `json:"phase,omitempty"`
	PendingSlot     string            `json:"pending_slot,omitempty"`
	PendingVersion  string            `json:"pending_version,omitempty"`
}

type Runner interface {
	Run(ctx context.Context, dir string, environment []string, binary string, args ...string) error
	Output(ctx context.Context, dir string, environment []string, binary string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, environment []string, binary string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = environment
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", binary, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
func (ExecRunner) Output(ctx context.Context, dir string, environment []string, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = environment
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %v: %w: %s", binary, args, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

type Controller struct {
	Root        string
	EnvFile     string
	Runner      Runner
	WaitTimeout time.Duration
}

var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var servicePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
var digestPattern = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)

func ParseRelease(path string) (Release, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Release{}, err
	}
	var r Release
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Release{}, fmt.Errorf("release manifest: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Release{}, errors.New("release manifest has trailing content")
	}
	return r, nil
}
func Validate(r Release) error {
	if !projectPattern.MatchString(r.Project) {
		return fmt.Errorf("invalid project name %q", r.Project)
	}
	if !versionPattern.MatchString(r.Version) {
		return fmt.Errorf("invalid version %q", r.Version)
	}
	if len(r.Images) == 0 {
		return errors.New("no images provided")
	}
	for s, img := range r.Images {
		if !servicePattern.MatchString(s) {
			return fmt.Errorf("invalid service %q", s)
		}
		if !digestPattern.MatchString(img) {
			return fmt.Errorf("image for %s must be pinned to a sha256 digest", s)
		}
	}
	if len(r.Routes) == 0 {
		return errors.New("no public routes specified")
	}
	hosts := map[string]bool{}
	for _, rt := range r.Routes {
		if !hostPattern.MatchString(rt.Host) || strings.Contains(rt.Host, "..") {
			return fmt.Errorf("invalid route host %q", rt.Host)
		}
		if !servicePattern.MatchString(rt.Service) {
			return fmt.Errorf("invalid routed service %q", rt.Service)
		}
		if _, ok := r.Images[rt.Service]; !ok {
			return fmt.Errorf("route for %q has no image", rt.Service)
		}
		if rt.Port < 1 || rt.Port > 65535 {
			return fmt.Errorf("invalid route port %d", rt.Port)
		}
		if hosts[strings.ToLower(rt.Host)] {
			return fmt.Errorf("duplicate hostname %s", rt.Host)
		}
		hosts[strings.ToLower(rt.Host)] = true
	}
	return nil
}

func (c Controller) defaults() Controller {
	if c.Runner == nil {
		c.Runner = ExecRunner{}
	}
	if c.WaitTimeout <= 0 {
		c.WaitTimeout = 120 * time.Second
	}
	return c
}
func (c Controller) paths() (root, app, infra, stateDir string, err error) {
	root, err = filepath.Abs(c.Root)
	if err != nil {
		return "", "", "", "", err
	}
	app = filepath.Join(root, "deploy", "compose.yml")
	infra = filepath.Join(root, "deploy", "infrastructure", "compose.yml")
	for _, p := range []string{app, infra} {
		if _, err = os.Stat(p); err != nil {
			return "", "", "", "", fmt.Errorf("missing Compose file %s: %w", p, err)
		}
	}
	return root, app, infra, filepath.Join(root, ".bluegreen"), nil
}
func (c Controller) environment(r Release, slot string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "PROJECT_NAME="+r.Project, "DEPLOYMENT_SLOT="+slot)
	services := make([]string, 0, len(r.Images))
	for s := range r.Images {
		services = append(services, s)
	}
	sort.Strings(services)
	for _, s := range services {
		env = append(env, strings.ToUpper(strings.ReplaceAll(s, "-", "_"))+"_IMAGE="+r.Images[s])
	}
	if img, ok := r.Images["app"]; ok {
		env = append(env, "APPLICATION_IMAGE="+img)
	}
	return env
}
func (c Controller) composeArgs(path string, args ...string) []string {
	opts := []string{"compose"}
	if c.EnvFile != "" {
		opts = append(opts, "--env-file", c.EnvFile)
	}
	opts = append(opts, "-f", path)
	return append(opts, args...)
}
func (c Controller) run(ctx context.Context, root, path string, r Release, slot string, args ...string) error {
	return c.Runner.Run(ctx, root, c.environment(r, slot), "docker", c.composeArgs(path, args...)...)
}
func (c Controller) output(ctx context.Context, root, path string, r Release, slot string, args ...string) (string, error) {
	return c.Runner.Output(ctx, root, c.environment(r, slot), "docker", c.composeArgs(path, args...)...)
}
func (c Controller) lock(stateDir string) (func(), error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another deployment is running: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func readState(dir string) (State, error) {
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("invalid persisted deployment state: %w", err)
	}
	return s, nil
}
func atomicWrite(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".bluegreen-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func saveState(dir string, s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "state.json"), append(b, '\n'), 0600)
}
func routeFile(root, project string) string {
	return filepath.Join(root, "deploy", "infrastructure", "caddy", "routes", project+".caddy")
}
func renderRoutes(project, slot string, routes []Route) []byte {
	cp := append([]Route(nil), routes...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Host < cp[j].Host })
	var b strings.Builder
	for _, rt := range cp {
		fmt.Fprintf(&b, "%s {\n    encode zstd gzip\n    reverse_proxy %s-%s-%s:%d\n}\n\n", rt.Host, project, slot, rt.Service, rt.Port)
	}
	return []byte(b.String())
}
func (c Controller) setRoutes(ctx context.Context, root, infra string, r Release, slot string, routes []Route) error {
	path := routeFile(root, r.Project)
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next := renderRoutes(r.Project, slot, routes)
	if len(next) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := atomicWrite(path, next, 0644); err != nil {
		return err
	}
	validate := c.run(ctx, root, infra, r, slot, "exec", "-T", "caddy", "caddy", "validate", "--config", "/etc/caddy/Caddyfile")
	if validate == nil {
		validate = c.run(ctx, root, infra, r, slot, "exec", "-T", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
	}
	if validate == nil {
		return nil
	}
	// Restore the on-disk route, then reload the previous configuration.
	if existed {
		_ = atomicWrite(path, old, 0644)
	} else {
		_ = os.Remove(path)
	}
	if restoreErr := c.run(ctx, root, infra, r, slot, "exec", "-T", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile"); restoreErr != nil {
		return fmt.Errorf("switch failed: %v; old Caddy configuration could not be reloaded: %w", validate, restoreErr)
	}
	return validate
}
func hash(r Release) string {
	b, _ := json.Marshal(struct {
		Version string
		Images  map[string]string
		Routes  []Route
	}{r.Version, r.Images, r.Routes})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func otherSlot(active string) string {
	if active == "blue" {
		return "green"
	}
	return "blue"
}
func (c Controller) verifyHealth(ctx context.Context, root, app string, r Release, slot string) error {
	services := make([]string, 0, len(r.Images))
	for svc := range r.Images {
		services = append(services, svc)
	}
	sort.Strings(services)
	for _, svc := range services {
		id, err := c.output(ctx, root, app, r, slot, "ps", "-q", svc)
		if err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("service %s is not running", svc)
		}
		status, err := c.Runner.Output(ctx, root, c.environment(r, slot), "docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", id)
		if err != nil {
			return err
		}
		if strings.TrimSpace(status) != "healthy" {
			return fmt.Errorf("service %s is not healthy (health status: %s)", svc, status)
		}
	}
	return nil
}
func (c Controller) reconcile(ctx context.Context, root, infra, stateDir string, r Release, s *State) error {
	if s.Phase == "" {
		return nil
	}
	if err := c.setRoutes(ctx, root, infra, r, s.ActiveSlot, s.ActiveRoutes); err != nil {
		return fmt.Errorf("recover interrupted switch: %w", err)
	}
	s.Phase = ""
	s.PendingSlot = ""
	s.PendingVersion = ""
	return saveState(stateDir, *s)
}
func (c Controller) Deploy(ctx context.Context, r Release) error {
	c = c.defaults()
	if err := Validate(r); err != nil {
		return err
	}
	root, app, infra, dir, err := c.paths()
	if err != nil {
		return err
	}
	unlock, err := c.lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := readState(dir)
	if err != nil {
		return err
	}
	if s.ActiveHash == hash(r) && s.ActiveVersion == r.Version && s.Phase == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(routeFile(root, r.Project)), 0750); err != nil {
		return fmt.Errorf("prepare Caddy route directory: %w", err)
	}
	if err = c.run(ctx, root, infra, r, "blue", "up", "-d", "--wait", "--wait-timeout", strconv.Itoa(int(c.WaitTimeout.Seconds()))); err != nil {
		return fmt.Errorf("start infrastructure: %w", err)
	}
	if err = c.reconcile(ctx, root, infra, dir, r, &s); err != nil {
		return err
	}
	slot := otherSlot(s.ActiveSlot)
	s.Phase = "preparing"
	s.PendingSlot = slot
	s.PendingVersion = r.Version
	if err = saveState(dir, s); err != nil {
		return err
	}
	// Do not rebuild anything on the host; release images must already exist in the registry.
	if err = c.run(ctx, root, app, r, slot, "pull"); err != nil {
		return fmt.Errorf("pull release images: %w", err)
	}
	if err = c.run(ctx, root, app, r, slot, "up", "-d", "--no-build", "--wait", "--wait-timeout", strconv.Itoa(int(c.WaitTimeout.Seconds()))); err != nil {
		return fmt.Errorf("start inactive slot: %w", err)
	}
	if err = c.verifyHealth(ctx, root, app, r, slot); err != nil {
		return fmt.Errorf("validate inactive slot: %w", err)
	}
	s.Phase = "switching"
	if err = saveState(dir, s); err != nil {
		return err
	}
	if err = c.setRoutes(ctx, root, infra, r, slot, r.Routes); err != nil {
		return fmt.Errorf("switch Caddy traffic: %w", err)
	}
	next := State{ActiveSlot: slot, ActiveVersion: r.Version, ActiveHash: hash(r), ActiveRoutes: r.Routes, PreviousSlot: s.ActiveSlot, PreviousVersion: s.ActiveVersion, PreviousHash: s.ActiveHash, PreviousRoutes: s.ActiveRoutes, ActiveImages: r.Images, PreviousImages: s.ActiveImages}
	if err = saveState(dir, next); err != nil {
		return fmt.Errorf("Caddy switched but state persistence failed; next invocation will reconcile: %w", err)
	}
	return nil
}
func (c Controller) Rollback(ctx context.Context, project string) error {
	c = c.defaults()
	if !projectPattern.MatchString(project) {
		return fmt.Errorf("invalid project %q", project)
	}
	root, app, infra, dir, err := c.paths()
	if err != nil {
		return err
	}
	unlock, err := c.lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := readState(dir)
	if err != nil {
		return err
	}
	if s.Phase != "" {
		return errors.New("incomplete deployment; retry deploy to reconcile before rollback")
	}
	if s.PreviousSlot == "" || s.PreviousVersion == "" || len(s.PreviousImages) == 0 {
		return errors.New("no previous release available for rollback")
	}
	// Previous slot remains running. Require all of its containers to be healthy.
	previous := Release{Project: project, Version: s.PreviousVersion, Images: s.PreviousImages, Routes: s.PreviousRoutes}
	if err := c.verifyHealth(ctx, root, app, previous, s.PreviousSlot); err != nil {
		return fmt.Errorf("previous release is unhealthy: %w", err)
	}
	if err = c.setRoutes(ctx, root, infra, previous, s.PreviousSlot, s.PreviousRoutes); err != nil {
		return err
	}
	restored := State{ActiveSlot: s.PreviousSlot, ActiveVersion: s.PreviousVersion, ActiveHash: s.PreviousHash, ActiveRoutes: s.PreviousRoutes, PreviousSlot: s.ActiveSlot, PreviousVersion: s.ActiveVersion, PreviousHash: s.ActiveHash, PreviousRoutes: s.ActiveRoutes, ActiveImages: s.PreviousImages, PreviousImages: s.ActiveImages}
	return saveState(dir, restored)
}
