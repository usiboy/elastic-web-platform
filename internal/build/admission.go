package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrIsolationUnavailable = errors.New("independent gVisor runtime unavailable")

// RequireRunsc checks Docker's declared runtimes before any tenant code runs.
// This is a necessary preflight, not proof of a trusted Builder node, signed
// image, enforced disk limit or an attested build result.
func RequireRunsc(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return ErrIsolationUnavailable
	}
	var runtimes map[string]struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &runtimes); err != nil {
		return ErrIsolationUnavailable
	}
	runtime, ok := runtimes["runsc"]
	if !ok || runtime.Path == "" || (runtime.Path != "runsc" && (!filepath.IsAbs(runtime.Path) || filepath.Base(runtime.Path) != "runsc")) || strings.ContainsAny(runtime.Path, "\x00\n\r") {
		return ErrIsolationUnavailable
	}
	return nil
}

// ProbeDocker inspects only the configured Docker daemon. It must be called
// on an independently approved Builder, never used to infer that the control
// ECS can execute arbitrary tenant scripts.
func ProbeDocker(ctx context.Context) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		return nil, fmt.Errorf("%w: Docker runtime probe failed", ErrIsolationUnavailable)
	}
	return output, nil
}
