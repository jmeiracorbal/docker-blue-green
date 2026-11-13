package openbao

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Manifest declares read-only references; it never contains secret values.
// The KV v2 API path is: /v1/{mount}/data/projects/{project}/{path}.
type Manifest struct {
	Mount   string      `json:"mount"`
	Secrets []Reference `json:"secrets"`
}

type Reference struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Field   string `json:"field"`
}

var (
	projectName   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	componentName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	serviceName   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	fieldName     = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
)

// Parse validates the complete configuration and rejects unknown fields.
func (m Manifest) Validate(project string) error {
	if !projectName.MatchString(project) {
		return fmt.Errorf("invalid OpenBao project name %q", project)
	}
	if !componentName.MatchString(m.Mount) {
		return errors.New("OpenBao KV v2 mount must be explicitly configured")
	}
	if len(m.Secrets) == 0 {
		return errors.New("OpenBao references contain no secrets")
	}
	seen := make(map[string]struct{}, len(m.Secrets))
	for _, ref := range m.Secrets {
		if !serviceName.MatchString(ref.Service) {
			return fmt.Errorf("invalid OpenBao service name %q", ref.Service)
		}
		if !componentName.MatchString(ref.Name) {
			return fmt.Errorf("invalid OpenBao secret name %q", ref.Name)
		}
		if ref.Path == "" {
			return fmt.Errorf("OpenBao secret %q has no path", ref.Name)
		}
		for _, component := range strings.Split(ref.Path, "/") {
			if !componentName.MatchString(component) {
				return fmt.Errorf("invalid path in OpenBao reference %q", ref.Name)
			}
		}
		if !fieldName.MatchString(ref.Field) {
			return fmt.Errorf("invalid field in OpenBao reference %q", ref.Name)
		}
		key := ref.Service + "/" + ref.Name
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate OpenBao reference %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}
