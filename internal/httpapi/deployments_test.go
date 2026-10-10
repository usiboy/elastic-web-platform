package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/uploads"
)

func deploymentSource(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAgentStaticUploadCreatesOnlyARecoverableCandidate(t *testing.T) {
	db := testDatabase(t)
	sourceDir := t.TempDir()
	s := uploads.NewDiskStore(sourceDir)
	artifacts := bundle.NewDiskStore(t.TempDir())
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(s), WithArtifactStore(artifacts), WithPreviewSuffix("preview.test"))
	team, owner := createTeam(t, h, "deploy-team")
	_, otherOwner := createTeam(t, h, "other-deploy-team")
	path := "/v1/teams/" + team + "/projects"
	_, project := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	p := project["id"].(string)
	_, token := request(t, h, http.MethodPost, "/v1/teams/"+team+"/tokens", owner, "agent", map[string]any{"role": "agent", "projectId": p, "actions": []string{"sources:upload", "deployments:create"}})
	agent := token["token"].(string)
	source := deploymentSource(t, map[string]string{"index.html": "<h1>Agent website</h1>", "app.js": "console.log('hi')"})
	code, uploaded := uploadSource(t, h, path+"/"+p+"/sources/uploads", agent, "source", source)
	if code != 201 {
		t.Fatalf("upload: %d %v", code, uploaded)
	}
	deployPath := path + "/" + p + "/deployments"
	code, first := request(t, h, http.MethodPost, deployPath, agent, "deploy-one", map[string]string{"sourceId": uploaded["sourceId"].(string)})
	if code != 202 || first["desiredState"] != "candidate" || first["observedState"] != "not_deployed" || first["deploymentId"] == "" {
		t.Fatalf("candidate: %d %v", code, first)
	}
	if !strings.HasSuffix(first["desiredHost"].(string), ".preview.test") {
		t.Fatalf("unexpected desired Host: %v", first)
	}
	code, same := request(t, h, http.MethodPost, deployPath, agent, "deploy-one", map[string]string{"sourceId": uploaded["sourceId"].(string)})
	if code != 200 || same["deploymentId"] != first["deploymentId"] {
		t.Fatalf("retry: %d %v", code, same)
	}
	otherSource := deploymentSource(t, map[string]string{"index.html": "<h1>Other</h1>"})
	_, otherUpload := uploadSource(t, h, path+"/"+p+"/sources/uploads", agent, "other-source", otherSource)
	if code, _ := request(t, h, http.MethodPost, deployPath, agent, "deploy-one", map[string]string{"sourceId": otherUpload["sourceId"].(string)}); code != 409 {
		t.Fatalf("same key different source: %d", code)
	}
	_, viewer := request(t, h, http.MethodPost, "/v1/teams/"+team+"/tokens", owner, "viewer-deploy", map[string]string{"role": "viewer"})
	if code, _ := request(t, h, http.MethodPost, deployPath, viewer["token"].(string), "viewer", map[string]string{"sourceId": uploaded["sourceId"].(string)}); code != 403 {
		t.Fatalf("viewer deployment: %d", code)
	}
	if code, _ := request(t, h, http.MethodPost, deployPath, otherOwner, "other-team", map[string]string{"sourceId": uploaded["sourceId"].(string)}); code != 404 {
		t.Fatalf("other Team deployment: %d", code)
	}
	if code, got := request(t, h, http.MethodGet, deployPath+"/"+first["deploymentId"].(string), owner, "", nil); code != 200 || got["observedState"] != "not_deployed" {
		t.Fatalf("deployment observed before route: %d %v", code, got)
	}
	if code, pj := request(t, h, http.MethodGet, path+"/"+p, owner, "", nil); code != 200 || pj["status"] != "not_deployed" {
		t.Fatalf("candidate activated project: %d %v", code, pj)
	}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM site_deployments WHERE team_id=$1 AND project_id=$2", team, p).Scan(&count); err != nil || count != 1 {
		t.Fatalf("candidate count: %d %v", count, err)
	}
	// A restart must be able to read the candidate and the exact static bytes.
	h = NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(sourceDir)), WithArtifactStore(artifacts), WithPreviewSuffix("preview.test"))
	if code, _ := request(t, h, http.MethodGet, deployPath+"/"+first["deploymentId"].(string), owner, "", nil); code != 200 {
		t.Fatalf("restart lost candidate: %d", code)
	}
}

func TestViteUploadCannotBypassIsolatedBuild(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(t.TempDir())), WithArtifactStore(bundle.NewDiskStore(t.TempDir())), WithPreviewSuffix("preview.test"))
	a, owner := createTeam(t, h, "vite-deploy")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "vite", "name": "Vite"})
	id := p["id"].(string)
	data := deploymentSource(t, map[string]string{"index.html": "<div id='app'></div>", "package.json": `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0","vite":"6.0.0"}}`, "package-lock.json": `{"lockfileVersion":3,"packages":{}}`})
	_, src := uploadSource(t, h, path+"/"+id+"/sources/uploads", owner, "source", data)
	if code, _ := request(t, h, http.MethodPost, path+"/"+id+"/deployments", owner, "deploy", map[string]string{"sourceId": src["sourceId"].(string)}); code != 409 {
		t.Fatalf("unisolated Vite build accepted: %d", code)
	}
}

func TestStaticDeploymentRejectsHiddenSourceFilesWithoutPublishing(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(t.TempDir())), WithArtifactStore(bundle.NewDiskStore(t.TempDir())), WithPreviewSuffix("preview.test"))
	a, owner := createTeam(t, h, "private-file-team")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	id := p["id"].(string)
	_, src := uploadSource(t, h, path+"/"+id+"/sources/uploads", owner, "source", deploymentSource(t, map[string]string{"index.html": "<h1>Site</h1>", ".env": "PRIVATE_KEY=value"}))
	if code, body := request(t, h, http.MethodPost, path+"/"+id+"/deployments", owner, "deploy", map[string]string{"sourceId": src["sourceId"].(string)}); code != 422 || body["error"].(map[string]any)["code"] != "static_source_rejected" {
		t.Fatalf("hidden file accepted or error mislabeled: %d %v", code, body)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM site_deployments WHERE project_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unsafe source produced deployment: %d %v", count, err)
	}
}

func TestDeploymentHistoryPaginatesRealProjectCandidates(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(t.TempDir())), WithArtifactStore(bundle.NewDiskStore(t.TempDir())), WithPreviewSuffix("preview.test"))
	a, owner := createTeam(t, h, "history-team")
	b, other := createTeam(t, h, "history-other")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	id := p["id"].(string)
	_, src := uploadSource(t, h, path+"/"+id+"/sources/uploads", owner, "source", deploymentSource(t, map[string]string{"index.html": "<h1>Site</h1>"}))
	listPath := path + "/" + id + "/deployments"
	for i := 0; i < 22; i++ {
		if code, _ := request(t, h, http.MethodPost, listPath, owner, fmt.Sprintf("attempt-%d", i), map[string]string{"sourceId": src["sourceId"].(string)}); code != 202 {
			t.Fatalf("create attempt %d: %d", i, code)
		}
	}
	code, first := request(t, h, http.MethodGet, listPath, owner, "", nil)
	if code != 200 || len(first["deployments"].([]any)) != 20 || first["hasMore"] != true || first["nextCursor"] == nil {
		t.Fatalf("first page: %d %v", code, first)
	}
	code, second := request(t, h, http.MethodGet, listPath+"?cursor="+first["nextCursor"].(string), owner, "", nil)
	if code != 200 || len(second["deployments"].([]any)) != 2 || second["hasMore"] != false {
		t.Fatalf("second page: %d %v", code, second)
	}
	if first["deployments"].([]any)[0].(map[string]any)["deploymentId"] == second["deployments"].([]any)[0].(map[string]any)["deploymentId"] {
		t.Fatal("page repeated a Deployment")
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/teams/"+b+"/projects/"+id+"/deployments", other, "", nil); code != 404 {
		t.Fatalf("cross-Team history: %d", code)
	}
	_, viewer := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "viewer-history", map[string]string{"role": "viewer"})
	if code, _ := request(t, h, http.MethodGet, listPath, viewer["token"].(string), "", nil); code != 200 {
		t.Fatalf("Viewer cannot inspect history: %d", code)
	}
	_, agent := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "agent-history", map[string]any{"role": "agent", "projectId": id, "actions": []string{"deployments:read"}})
	if code, _ := request(t, h, http.MethodGet, listPath, agent["token"].(string), "", nil); code != 200 {
		t.Fatalf("scoped Agent cannot inspect history: %d", code)
	}
}
