package openbao

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Writer uses a developer's HTTPS OpenBao session, never the deployment proxy.
type Writer struct {
	http *http.Client
	base string
	token string
}

// NewWriter accepts the standard BAO_ADDR, BAO_TOKEN, BAO_TOKEN_PATH and
// BAO_CACERT settings. No credentials are accepted as command-line arguments.
func NewWriter() (*Writer, error) {
	address := strings.TrimSpace(os.Getenv("BAO_ADDR"))
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("BAO_ADDR must be an HTTPS OpenBao origin")
	}
	token := strings.TrimSpace(os.Getenv("BAO_TOKEN"))
	if token == "" {
		path := os.Getenv("BAO_TOKEN_PATH")
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil { return nil, errors.New("cannot locate the OpenBao login session") }
			path = filepath.Join(home, ".vault-token")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("OpenBao login token file is unavailable or not regular")
		}
		data, err := os.ReadFile(path)
		if err != nil { return nil, errors.New("cannot read the OpenBao login session") }
		token = strings.TrimSpace(string(data))
	}
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("an authenticated OpenBao session is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caPath := os.Getenv("BAO_CACERT"); caPath != "" {
		info, err := os.Lstat(caPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("BAO_CACERT must identify a regular PEM file")
		}
		caData, err := os.ReadFile(caPath)
		if err != nil { return nil, errors.New("cannot read OpenBao CA certificate") }
		pool, err := x509.SystemCertPool()
		if err != nil { pool = x509.NewCertPool() }
		if !pool.AppendCertsFromPEM(caData) {
			return nil, errors.New("invalid OpenBao CA certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Writer{
		http: &http.Client{
			Transport: transport,
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("OpenBao redirects are forbidden")
			},
		},
		base: strings.TrimRight(u.String(), "/"),
		token: token,
	}, nil
}

// PutFields writes a single KV v2 object with CAS. Existing unrelated fields
// are preserved, so an import cannot accidentally delete another field.
// Returns false when all imported fields already have identical values.
func (w *Writer) PutFields(ctx context.Context, project, mount, secretPath string, values map[string]string, allowUpdate bool) (bool, error) {
	reference := Reference{Service: "app", Name: "import", Path: secretPath, Field: "value"}
	if err := (Manifest{Mount: mount, Secrets: []Reference{reference}}).Validate(project); err != nil {
		return false, err
	}
	if len(values) == 0 || len(values) > 512 {
		return false, errors.New("OpenBao import requires 1 to 512 fields")
	}
	for name, value := range values {
		if !fieldName.MatchString(name) || value == "" {
			return false, errors.New("invalid or empty OpenBao import field")
		}
	}
	path := "/v1/" + mount + "/data/projects/" + project + "/" + secretPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.base+path, nil)
	if err != nil { return false, errors.New("cannot construct OpenBao read request") }
	data, status, err := w.request(request)
	if err != nil { return false, err }
	version := 0
	merged := make(map[string]string, len(values))
	switch status {
	case http.StatusNotFound:
		// The first version must use CAS=0.
	case http.StatusOK:
		var current struct {
			Data struct {
				Data map[string]string `json:"data"`
				Metadata struct { Version int `json:"version"` } `json:"metadata"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &current); err != nil ||
			current.Data.Metadata.Version <= 0 || current.Data.Data == nil {
			return false, errors.New("invalid OpenBao KV v2 data response")
		}
		version = current.Data.Metadata.Version
		for key, value := range current.Data.Data { merged[key] = value }
	default:
		return false, fmt.Errorf("OpenBao KV read failed (HTTP %d)", status)
	}
	changed := status == http.StatusNotFound
	for key, value := range values {
		if previous, exists := merged[key]; !exists || previous != value {
			changed = true
		}
		merged[key] = value
	}
	if !changed { return false, nil }
	if version != 0 && !allowUpdate {
		return false, errors.New("OpenBao fields differ; rerun with --update to authorize the new version")
	}
	body, err := json.Marshal(struct {
		Data map[string]string `json:"data"`
		Options struct { CAS int `json:"cas"` } `json:"options"`
	}{Data: merged, Options: struct { CAS int `json:"cas"` }{CAS: version}})
	if err != nil { return false, errors.New("cannot encode OpenBao KV request") }
	write, err := http.NewRequestWithContext(ctx, http.MethodPost, w.base+path, bytes.NewReader(body))
	if err != nil { return false, errors.New("cannot construct OpenBao write request") }
	write.Header.Set("Content-Type", "application/json")
	_, status, err = w.request(write)
	if err != nil { return false, err }
	if status != http.StatusOK && status != http.StatusNoContent {
		return false, fmt.Errorf("OpenBao CAS write rejected (HTTP %d)", status)
	}
	return true, nil
}

func (w *Writer) request(req *http.Request) ([]byte, int, error) {
	req.Header.Set("X-Vault-Token", w.token)
	req.Header.Set("X-Vault-Request", "true")
	res, err := w.http.Do(req)
	if err != nil { return nil, 0, errors.New("cannot reach the authenticated OpenBao HTTPS endpoint") }
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, 0, errors.New("invalid or oversized OpenBao response")
	}
	return data, res.StatusCode, nil
}
