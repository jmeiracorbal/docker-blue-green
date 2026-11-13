package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// ListKeys discovers only names beneath a project's KV v2 metadata prefix.
// No other project's keys are requested, and secret values are never returned.
func (c *Client) ListKeys(ctx context.Context, project, mount string) ([]string, error) {
	if !projectName.MatchString(project) || !componentName.MatchString(mount) {
		return nil, errors.New("invalid OpenBao project or KV v2 mount")
	}
	var paths []string
	var visit func(string) error
	visited := make(map[string]bool)
	visit = func(folder string) error {
		if visited[folder] || len(visited) >= 512 {
			return errors.New("OpenBao metadata listing is cyclic or exceeds 512 directories")
		}
		visited[folder] = true
		path := "/v1/" + mount + "/metadata/projects/" + project
		if folder != "" {
			path += "/" + folder
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://openbao-proxy"+path+"?list=true", nil)
		if err != nil {
			return errors.New("invalid OpenBao metadata request")
		}
		response, err := c.http.Do(req)
		if err != nil {
			return errors.New("cannot connect to OpenBao Proxy")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("OpenBao metadata listing failed (HTTP %d)", response.StatusCode)
		}
		var body struct {
			Data struct {
				Keys []string `json:"keys"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
			return errors.New("invalid OpenBao metadata response")
		}
		for _, key := range body.Data.Keys {
			isDirectory := strings.HasSuffix(key, "/")
			key = strings.TrimSuffix(key, "/")
			if !componentName.MatchString(key) {
				return errors.New("invalid key name in OpenBao metadata response")
			}
			name := key
			if folder != "" {
				name = folder + "/" + key
			}
			if isDirectory {
				if err := visit(name); err != nil {
					return err
				}
			} else {
				if len(paths) >= 2048 {
					return errors.New("OpenBao metadata listing exceeds 2048 secrets")
				}
				paths = append(paths, name)
			}
		}
		return nil
	}
	if err := visit(""); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// ListFields retrieves one authorized KV v2 record and returns only the names
// of its string fields. It never writes or prints their secret values.
func (c *Client) ListFields(ctx context.Context, project, mount, secretPath string) ([]string, error) {
	ref := Reference{Service: "app", Name: "selection", Path: secretPath, Field: "value"}
	contract := Manifest{Mount: mount, Secrets: []Reference{ref}}
	if err := contract.Validate(project); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://openbao-proxy/v1/"+mount+"/data/projects/"+project+"/"+secretPath, nil)
	if err != nil {
		return nil, errors.New("invalid OpenBao KV request")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("cannot connect to OpenBao Proxy")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenBao KV read failed (HTTP %d)", response.StatusCode)
	}
	var payload struct {
		Data struct {
			Data map[string]json.RawMessage `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, errors.New("invalid OpenBao KV v2 response")
	}
	if payload.Data.Metadata.Version <= 0 || payload.Data.Data == nil {
		return nil, errors.New("invalid OpenBao KV v2 version")
	}
	var names []string
	for name, raw := range payload.Data.Data {
		if !fieldName.MatchString(name) {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil && value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
