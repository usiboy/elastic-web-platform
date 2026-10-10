package build

import (
	"errors"
	"testing"
)

func TestRunscAdmissionRejectsRuncAndRuntimeAliases(t *testing.T) {
	for _, info := range []string{
		`{"runc":{"path":"runc"}}`,
		`{"runsc":{"path":"/usr/bin/runc"},"runc":{"path":"runc"}}`,
		`{"runsc":{"path":"/usr/local/bin/runsc-wrapper"}}`,
		`{"runsc":{"path":"./runsc"}}`,
		`invalid json`,
	} {
		if err := RequireRunsc([]byte(info)); !errors.Is(err, ErrIsolationUnavailable) {
			t.Fatalf("untrusted runtime accepted for %q: %v", info, err)
		}
	}
	if err := RequireRunsc([]byte(`{"runsc":{"path":"/usr/local/bin/runsc"},"runc":{"path":"runc"}}`)); err != nil {
		t.Fatalf("installed gVisor runtime rejected: %v", err)
	}
}
