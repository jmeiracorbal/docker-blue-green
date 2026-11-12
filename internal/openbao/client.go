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

func NewClient(socket string) (*Client, error) {
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

// Check reads and verifies every required field without exposing its contents.
// It never prints or persists secret values.
func (c *Client) Check(ctx context.Context, project string, manifest Manifest) error {
	if err := manifest.Validate(project); err != nil {
		return err
	}
	for _, ref := range manifest.Secrets {
		if err := c.checkField(ctx, project, manifest.Mount, ref); err != nil {
			return fmt.Errorf("OpenBao reference %s/%s: %w", ref.Service, ref.Name, err)
		}
	}
	return nil
}

func (c *Client) checkField(ctx context.Context, project, mount string, ref Reference) error {
	apiPath := "/v1/" + mount + "/data/projects/" + project + "/" + ref.Path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://openbao-proxy"+apiPath, nil)
	if err != nil {
		return errors.New("invalid OpenBao API request")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("cannot connect to OpenBao Proxy")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Never include an arbitrary server response body: it may contain secrets.
		return fmt.Errorf("OpenBao request failed (HTTP %d)", response.StatusCode)
	}
	var payload struct {
		Data struct {
			Data     map[string]json.RawMessage `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("invalid OpenBao KV v2 response")
	}
	if payload.Data.Metadata.Version <= 0 || payload.Data.Data == nil {
		return errors.New("invalid OpenBao KV v2 secret version")
	}
	raw, ok := payload.Data.Data[ref.Field]
	if !ok {
		return fmt.Errorf("requested field %q is missing", ref.Field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return fmt.Errorf("requested field %q must contain a non-empty string", ref.Field)
	}
	return nil
}
