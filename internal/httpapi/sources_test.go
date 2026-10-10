package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"elastic-web-platform/internal/source/uploads"
)

func sourceBundle(t *testing.T, name, contents string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(contents)), Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func uploadSource(t *testing.T, handler http.Handler, path, token, key string, data []byte) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("status %d: invalid JSON: %s", w.Code, w.Body.String())
	}
	return w.Code, result
}

func TestAgentUploadStoresOnlyVerifiedBytesWithoutDeploying(t *testing.T) {
	db := testDatabase(t)
	root := t.TempDir()
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(root)))
	a, owner := createTeam(t, h, "source-team")
	b, bOwner := createTeam(t, h, "other-team")
	aPath := "/v1/teams/" + a + "/projects"
	bPath := "/v1/teams/" + b + "/projects"
	_, aProject := request(t, h, http.MethodPost, aPath, owner, "a-project", map[string]string{"slug": "site", "name": "Site"})
	_, bProject := request(t, h, http.MethodPost, bPath, bOwner, "b-project", map[string]string{"slug": "site", "name": "Site"})
	aID := aProject["id"].(string)
	uploadPath := aPath + "/" + aID + "/sources/uploads"
	_, agent := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "uploader", map[string]any{"role": "agent", "projectId": aID, "actions": []string{"sources:upload", "sources:read"}})
	if agent["token"] == nil {
		t.Fatalf("agent must be granted upload capability: %v", agent)
	}
	data := sourceBundle(t, "index.html", "<h1>Agent site</h1>")
	code, result := uploadSource(t, h, uploadPath, agent["token"].(string), "upload-a", data)
	if code != 201 || result["status"] != "stored_unverified" || result["sourceId"] == "" {
		t.Fatalf("upload: %d %v", code, result)
	}
	sum := sha256.Sum256(data)
	if result["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("wrong source hash: %v", result)
	}
	code, retry := uploadSource(t, h, uploadPath, agent["token"].(string), "upload-a", data)
	if code != 200 || retry["sourceId"] != result["sourceId"] {
		t.Fatalf("retry: %d %v", code, retry)
	}
	if code, _ := uploadSource(t, h, uploadPath, agent["token"].(string), "upload-a", sourceBundle(t, "index.html", "changed")); code != 409 {
		t.Fatalf("same key different bytes: %d", code)
	}
	getPath := aPath + "/" + aID + "/sources/" + result["sourceId"].(string)
	if code, got := request(t, h, http.MethodGet, getPath, agent["token"].(string), "", nil); code != 200 || got["sha256"] != result["sha256"] {
		t.Fatalf("agent read source metadata: %d %v", code, got)
	}
	if code, _ := request(t, h, http.MethodGet, bPath+"/"+bProject["id"].(string)+"/sources/"+result["sourceId"].(string), agent["token"].(string), "", nil); code != 404 {
		t.Fatalf("cross-team source: %d", code)
	}
	_, viewer := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "viewer-source", map[string]string{"role": "viewer"})
	if code, _ := uploadSource(t, h, uploadPath, viewer["token"].(string), "viewer-attempt", data); code != 403 {
		t.Fatalf("viewer uploaded source: %d", code)
	}
	if code, got := request(t, h, http.MethodGet, aPath+"/"+aID, owner, "", nil); code != 200 || got["status"] != "not_deployed" {
		t.Fatalf("upload activated project: %d %v", code, got)
	}
	h = NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(root)))
	if code, _ := request(t, h, http.MethodGet, getPath, owner, "", nil); code != 200 {
		t.Fatalf("source missing after restart: %d", code)
	}
	stored, err := uploads.NewDiskStore(root).Get(context.Background(), a, aID, result["sha256"].(string))
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("stored bytes mismatch: %v", err)
	}
}

func TestUploadRejectsUnsafeArchiveAndMissingStore(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(t.TempDir())))
	a, owner := createTeam(t, h, "invalid-source")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "site", map[string]string{"slug": "site", "name": "Site"})
	uploadPath := path + "/" + p["id"].(string) + "/sources/uploads"
	for _, data := range [][]byte{[]byte("not gzip"), sourceBundle(t, "../escape", "bad")} {
		if code, _ := uploadSource(t, h, uploadPath, owner, "invalid", data); code != 422 {
			t.Fatalf("unsafe archive accepted: %d", code)
		}
	}
	if code, _ := uploadSource(t, h, uploadPath, owner, "too-large", bytes.Repeat([]byte("x"), uploads.MaxArchiveBytes+1)); code != 413 {
		t.Fatalf("oversize archive: %d", code)
	}
	if code, _ := uploadSource(t, NewServer(db, "bootstrap-local-test"), uploadPath, owner, "no-store", sourceBundle(t, "index.html", "x")); code != 503 {
		t.Fatalf("unconfigured store: %d", code)
	}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM source_uploads").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid sources persisted: %d %v", count, err)
	}
}
