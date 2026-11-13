package deploy

import (
    "context"
    "errors"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
)

// ReconcileSecrets checks secret versions for the release currently active on
// this host. It does not use release.json: that file could contain a newer
// release intentionally rolled back by the operator.
func (c Controller) ReconcileSecrets(ctx context.Context, project string) error {
    if !projectPattern.MatchString(project) { return errors.New("invalid project name") }
    root, _, _, stateDir, err := c.paths()
    if err != nil { return err }
    state, err := readState(stateDir)
    if err != nil { return err }
    if state.AutoReconcilePaused { return nil }
    if state.ActiveVersion == "" || len(state.ActiveImages)==0 || len(state.ActiveRoutes)==0 {
        return errors.New("cannot reconcile before an initial deployment")
    }
    cfg, err := config.Load(root)
    if err != nil { return err }
    if cfg.Project != project { return errors.New("reconciliation project differs from deploy/bluegreen.yml") }
    if _, ok := cfg.Secrets(); !ok { return errors.New("OpenBao project has no secret references") }
    release:=Release{
        Project:project, Version:state.ActiveVersion,
        Images:state.ActiveImages, Routes:state.ActiveRoutes,
    }
    c.Automatic = true
    c.ExpectedActiveHash = state.ActiveHash
    return c.Deploy(ctx,release)
}
