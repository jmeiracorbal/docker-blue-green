package deploy

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strconv"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

// SecretResolver is injected only for tests. Production uses the local,
// project-scoped OpenBao Proxy and never accepts an endpoint from the release.
type SecretResolver func(context.Context, string, openbao.Manifest) ([]openbao.Resolved, error)

type secretSnapshot struct {
    manifest openbao.Manifest
    values []openbao.Resolved
    revision string
}

func (c Controller) loadSecrets(ctx context.Context, root string, r Release) (*secretSnapshot, error) {
    manifestPath := filepath.Join(root,"deploy","secrets.json")
    manifestBytes, err := os.ReadFile(manifestPath)
    if errors.Is(err, os.ErrNotExist) { return nil,nil }
    if err != nil { return nil,fmt.Errorf("read OpenBao references: %w",err) }
    manifest, err := openbao.Parse(manifestBytes,r.Project)
    if err != nil { return nil,err }
    for _, ref := range manifest.Secrets {
        if _, exists := r.Images[ref.Service]; !exists {
            return nil,fmt.Errorf("OpenBao reference targets unknown application service %q",ref.Service)
        }
    }
    var values []openbao.Resolved
    if c.SecretResolver != nil {
        values, err = c.SecretResolver(ctx,r.Project,manifest)
    } else {
        var client *openbao.Client
        client, err = openbao.NewClient(r.Project)
        if err == nil { values,err=client.Resolve(ctx,r.Project,manifest) }
    }
    if err != nil { return nil,err }
    if len(values) != len(manifest.Secrets) { return nil,errors.New("OpenBao resolver returned an incomplete secret set") }
    versions:=make([]string,0,len(values))
    seen:=make(map[string]bool,len(values))
    for _, value := range values {
        if value.Version<1 || len(value.Data)==0 { return nil,errors.New("OpenBao resolver returned an invalid secret") }
        key:=value.Reference.Service+"/"+value.Reference.Name
        if seen[key] { return nil,errors.New("OpenBao resolver returned duplicate references") }
        seen[key]=true
        matched:=false
        for _, ref:=range manifest.Secrets { if ref==value.Reference { matched=true;break } }
        if !matched { return nil,errors.New("OpenBao resolver returned an unexpected reference") }
        versions=append(versions,key+"/"+value.Reference.Path+"/"+value.Reference.Field+"@"+strconv.Itoa(value.Version))
    }
    sort.Strings(versions)
    // KV v2 versions, not secret values, participate in deployment identity.
    versionManifest,err:=json.Marshal(struct{
        Manifest openbao.Manifest
        Versions []string
    }{manifest,versions})
    if err!=nil{return nil,err}
    digest:=sha256.Sum256(versionManifest)
    return &secretSnapshot{manifest:manifest,values:values,revision:hex.EncodeToString(digest[:])},nil
}

func releaseHashWithSecrets(r Release, snapshot *secretSnapshot) string {
    if snapshot==nil{return hash(r)}
    h:=sha256.Sum256([]byte(hash(r)+":"+snapshot.revision))
    return hex.EncodeToString(h[:])
}

// stageSecretOverlay materializes a versioned, project-private snapshot.
// Files must persist on the Docker host for restart and rollback. The source
// directory is 0700; leaf files are 0444 because Compose bind mounts do not
// support UID remapping, and application UIDs vary.
func stageSecretOverlay(root,slot string,snapshot *secretSnapshot) (string,error) {
    if snapshot==nil{return "",nil}
    dir:=filepath.Join(root,".bluegreen","secrets",slot,snapshot.revision)
    if err:=os.MkdirAll(dir,0700);err!=nil{return "",err}
    if err:=os.Chmod(dir,0700);err!=nil{return "",err}
    services:=map[string]map[string]any{}
    topSecrets:=map[string]any{}
    for _,v:=range snapshot.values {
        serviceDir:=filepath.Join(dir,v.Reference.Service)
        if err:=os.MkdirAll(serviceDir,0700);err!=nil{return "",err}
        if err:=os.Chmod(serviceDir,0700);err!=nil{return "",err}
        secretFile:=filepath.Join(serviceDir,v.Reference.Name)
        if err:=writeSecretOnce(secretFile,v.Data);err!=nil{return "",err}
        source:=v.Reference.Service+"_"+v.Reference.Name
        topSecrets[source]=map[string]any{"file":secretFile}
        item,ok:=services[v.Reference.Service]
        if !ok {item=map[string]any{"secrets":[]any{}};services[v.Reference.Service]=item}
        item["secrets"]=append(item["secrets"].([]any),map[string]string{
            "source":source,
            "target":v.Reference.Name,
        })
    }
    overlay:=map[string]any{"services":services,"secrets":topSecrets}
    data,err:=json.MarshalIndent(overlay,"","  ")
    if err!=nil{return "",err}
    path:=filepath.Join(dir,"compose.json")
    if err:=writeSnapshotOnce(path,append(data,'\n'),0600);err!=nil{return "",err}
    return path,nil
}

func writeSecretOnce(path string,data []byte) error {
    return writeSnapshotOnce(path,data,0444)
}
func writeSnapshotOnce(path string,data []byte,mode os.FileMode) error {
    verifyExisting:=func() error {
        info,err:=os.Lstat(path)
        if err!=nil{return err}
        if !info.Mode().IsRegular() {return fmt.Errorf("unsafe secret snapshot: %s",path)}
        existing,err:=os.ReadFile(path)
        if err!=nil{return err}
        if !bytes.Equal(existing,data) {return fmt.Errorf("existing snapshot differs at %s",path)}
        return nil
    }
    if err:=verifyExisting();err==nil{return nil}else if !errors.Is(err,os.ErrNotExist){return err}
    tmp,err:=os.CreateTemp(filepath.Dir(path),".snapshot-")
    if err!=nil{return err}
    defer os.Remove(tmp.Name())
    if err:=tmp.Chmod(mode);err!=nil{tmp.Close();return err}
    if _,err:=tmp.Write(data);err!=nil{tmp.Close();return err}
    if err:=tmp.Sync();err!=nil{tmp.Close();return err}
    if err:=tmp.Close();err!=nil{return err}
    // Hard-link creation exposes the completed file atomically, without
    // replacing an already-mounted secret inode from another deployment.
    if err:=os.Link(tmp.Name(),path);errors.Is(err,os.ErrExist){return verifyExisting()}else if err!=nil{return err}
    return nil
}

// pruneSnapshots keeps the current and rollback-ready versions only.
// An interrupted deployment is recovered before any pruning is attempted.
func pruneSnapshots(root string,s State) error {
    keep:=map[string]string{
        s.ActiveSlot:s.ActiveSecretRevision,
        s.PreviousSlot:s.PreviousSecretRevision,
    }
    for _,slot:=range []string{"blue","green"} {
        base:=filepath.Join(root,".bluegreen","secrets",slot)
        entries,err:=os.ReadDir(base)
        if errors.Is(err,os.ErrNotExist){continue}
        if err!=nil{return err}
        for _,entry:=range entries {
            if !entry.IsDir() || entry.Name()==keep[slot] {continue}
            if err:=os.RemoveAll(filepath.Join(base,entry.Name()));err!=nil{return err}
        }
    }
    return nil
}
