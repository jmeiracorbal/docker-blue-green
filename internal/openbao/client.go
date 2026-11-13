package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client talks to one project's OpenBao Proxy Unix socket. The proxy performs
// auto-auth and enforces the project-specific OpenBao read policy.
type Client struct {
	http *http.Client
}

// ProxySocketPath is the fixed deployment endpoint for a project's proxy.
// Server provisioning must expose the proxy socket at this location.
func ProxySocketPath(project string) (string, error) {
	if !projectName.MatchString(project) {
		return "", fmt.Errorf("invalid OpenBao project name %q", project)
	}
	return filepath.Join("/run/bluegreen", project, "openbao.sock"), nil
}

// NewClient connects to the project's provisioned OpenBao Proxy endpoint.
func NewClient(project string) (*Client, error) {
	socket, err := ProxySocketPath(project)
	if err != nil {
		return nil, err
	}
	return newClientAtSocket(socket)
}

// newClientAtSocket allows internal tests to use a temporary Unix socket.
func newClientAtSocket(socket string) (*Client, error) {
	if socket == "" || !filepath.IsAbs(socket) {
		return nil, errors.New("OpenBao Proxy requires an absolute Unix socket path")
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return nil, fmt.Errorf("access OpenBao Proxy socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("OpenBao Proxy endpoint is not a Unix socket")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 10 * time.Second}}, nil
}

// Resolved is a secret read from OpenBao. Data must never be logged,
// serialized into deployment state, or included in error messages.
type Resolved struct {
    Reference Reference
    Data []byte
    Version int
}

// Resolve retrieves the validated KV v2 fields through the project's proxy.
func (c *Client) Resolve(ctx context.Context, project string, manifest Manifest) ([]Resolved, error) {
    if err := manifest.Validate(project); err != nil { return nil, err }
    resolved := make([]Resolved, 0, len(manifest.Secrets))
    for _, ref := range manifest.Secrets {
        value, version, err := c.readField(ctx, project, manifest.Mount, ref)
        if err != nil { return nil, fmt.Errorf("OpenBao reference %s/%s: %w", ref.Service, ref.Name, err) }
        resolved = append(resolved, Resolved{Reference:ref, Data:value, Version:version})
    }
    return resolved,nil
}

// Check verifies required fields without printing or persisting their values.
func (c *Client) Check(ctx context.Context, project string, manifest Manifest) error {
    _, err := c.Resolve(ctx, project, manifest)
    return err
}

func (c *Client) readField(ctx context.Context, project, mount string, ref Reference) ([]byte, int, error) {
    apiPath := "/v1/" + mount + "/data/projects/" + project + "/" + ref.Path
    request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://openbao-proxy"+apiPath, nil)
    if err != nil { return nil, 0, errors.New("invalid OpenBao API request") }
    response, err := c.http.Do(request)
    if err != nil { return nil, 0, errors.New("cannot connect to OpenBao Proxy") }
    defer response.Body.Close()
    if response.StatusCode != http.StatusOK {
        return nil, 0, fmt.Errorf("OpenBao request failed (HTTP %d)", response.StatusCode)
    }
    var payload struct {
        Data struct {
            Data map[string]json.RawMessage `json:"data"`
            Metadata struct { Version int `json:"version"` } `json:"metadata"`
        } `json:"data"`
    }
    decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
    if err := decoder.Decode(&payload); err != nil { return nil, 0, errors.New("invalid OpenBao KV v2 response") }
    if payload.Data.Metadata.Version <= 0 || payload.Data.Data == nil {
        return nil, 0, errors.New("invalid OpenBao KV v2 secret version")
    }
    raw, ok := payload.Data.Data[ref.Field]
    if !ok { return nil, 0, fmt.Errorf("requested field %q is missing", ref.Field) }
    var value string
    if err := json.Unmarshal(raw, &value); err != nil || value == "" {
        return nil, 0, fmt.Errorf("requested field %q must contain a non-empty string", ref.Field)
    }
    return []byte(value), payload.Data.Metadata.Version, nil
}
