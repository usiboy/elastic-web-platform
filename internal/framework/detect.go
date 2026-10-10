package framework

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"elastic-web-platform/internal/source/uploads"
)

type Config struct {
	Name          string `json:"name"`
	OutputDir     string `json:"outputDir"`
	BuildCommand  string `json:"buildCommand,omitempty"`
	RequiresBuild bool   `json:"requiresBuild"`
}

// Detect reads untrusted metadata only; it never installs dependencies or runs
// a project's build script. A recognized Vite source still requires a sandbox.
func Detect(archive []byte) (Config, error) {
	if _, err := uploads.Validate(archive); err != nil {
		return Config{}, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return Config{}, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var packageData, lockData []byte
	hasIndex := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Config{}, err
		}
		switch h.Name {
		case "index.html":
			hasIndex = true
		case "package.json":
			if h.Size > 64<<10 {
				return Config{}, errors.New("package manifest too large")
			}
			packageData, err = io.ReadAll(tr)
		case "package-lock.json":
			if h.Size > uploads.MaxArchiveBytes {
				return Config{}, errors.New("lockfile too large")
			}
			lockData, err = io.ReadAll(tr)
		}
		if err != nil {
			return Config{}, err
		}
	}
	if packageData == nil {
		if !hasIndex {
			return Config{}, errors.New("no supported website entry")
		}
		return Config{Name: "static-html", OutputDir: ".", RequiresBuild: false}, nil
	}
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(packageData, &pkg) != nil {
		return Config{}, errors.New("invalid package.json")
	}
	deps := map[string]string{}
	for k, v := range pkg.Dependencies {
		deps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		deps[k] = v
	}
	if deps["next"] != "" {
		return Config{}, errors.New("Next.js SSR is not available in the static release")
	}
	// The preset never executes npm's prebuild/postbuild hooks or extra shell
	// commands. Other build settings require a separately admitted builder.
	if deps["vite"] == "" || strings.TrimSpace(pkg.Scripts["build"]) != "vite build" {
		return Config{}, errors.New("unsupported build preset")
	}
	var lock struct {
		LockfileVersion int                        `json:"lockfileVersion"`
		Packages        map[string]json.RawMessage `json:"packages"`
	}
	if len(lockData) == 0 || json.Unmarshal(lockData, &lock) != nil || lock.LockfileVersion < 2 || lock.Packages == nil {
		return Config{}, errors.New("Vite build requires a supported npm lockfile")
	}
	switch {
	case deps["vue"] != "" && deps["react"] == "":
		return Config{Name: "vite-vue", OutputDir: "dist", BuildCommand: "./node_modules/.bin/vite build", RequiresBuild: true}, nil
	case deps["react"] != "" && deps["vue"] == "":
		return Config{Name: "vite-react", OutputDir: "dist", BuildCommand: "./node_modules/.bin/vite build", RequiresBuild: true}, nil
	default:
		return Config{}, errors.New("unsupported Vite framework")
	}
}
