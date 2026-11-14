package deploy

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "time"

    "github.com/jmeiracorbal/docker-blue-green/internal/config"
)

// lifecycleFingerprint ties an active release to the lifecycle contract that
// prepared it. A changed migration/check definition needs a new release.
func lifecycleFingerprint(lifecycle *config.Lifecycle) (string, error) {
    if lifecycle == nil { return "",nil }
    data,err:=json.Marshal(lifecycle)
    if err!=nil{return "",err}
    digest:=sha256.Sum256(data)
    return hex.EncodeToString(digest[:]),nil
}

// runLifecycle executes commands as argv, not through a host or container
// shell. The target is always the inactive application Compose project.
// Each operation is at-least-once across retries; migrations must be idempotent.
func (c Controller) runLifecycle(
    ctx context.Context, root, app string, release Release, slot, stage string,
    steps []config.LifecycleStep,
) error {
    for i,step:=range steps {
        stepCtx,cancel:=context.WithTimeout(ctx,time.Duration(step.TimeoutSeconds)*time.Second)
        args:=[]string{"run","--rm","--no-deps","-T","--pull","never","--entrypoint",step.Entrypoint,step.Service}
        args=append(args,step.Args...)
        err:=c.run(stepCtx,root,app,release,slot,args...)
        cancel()
        if err!=nil {
            return fmt.Errorf("lifecycle %s[%d] service %s: %w",stage,i,step.Service,err)
        }
    }
    return nil
}
