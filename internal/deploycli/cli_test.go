package deploycli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandValidation(t *testing.T) {
	for _, args := range [][]string{{}, {"deploy"}, {"deploy", "--release"}, {"rollback"}, {"rollback", "--project", ""}, {"invalid"}} {
		var out bytes.Buffer
		if err := Run(args, &out); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}
func TestInvalidReleaseStopsBeforeDocker(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"deploy", "--dir", t.TempDir(), "--release", "missing.json"}, &out); err == nil {
		t.Fatal("missing release accepted")
	}
	if out.Len() != 0 {
		t.Fatal("unexpected output", strings.TrimSpace(out.String()))
	}
}
