package deploy

import (
    "context"
    "errors"
    "fmt"
    "os"
    "path/filepath"
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
    if _, err:=os.Stat(filepath.Join(root,"deploy","secrets.json"));err!=nil {
        return fmt.Errorf("OpenBao project has no secret reference manifest: %w",err)
    }
    release:=Release{
        Project:project, Version:state.ActiveVersion,
        Images:state.ActiveImages, Routes:state.ActiveRoutes,
    }
    return c.Deploy(ctx,release)
}
