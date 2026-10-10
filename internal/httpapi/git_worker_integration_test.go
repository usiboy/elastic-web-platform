package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/pipeline"
	"elastic-web-platform/internal/routing"
	"elastic-web-platform/internal/source/uploads"
)

func TestSignedGitWebhookWorkerProducesPrivateCandidateAndUnobservedProduction(t *testing.T) {
	db := testDatabase(t)
	sources := uploads.NewDiskStore(t.TempDir())
	artifacts := bundle.NewDiskStore(t.TempDir())
	client, closeRemote := githubTestClient(t)
	defer closeRemote()
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(sources), WithArtifactStore(artifacts), WithPreviewSuffix("preview.test"), WithProductionSuffix("sites.test"), WithGitHubVerifier(client), WithGitHubWebhookSecret("webhook-test-secret"))
	a, owner := createTeam(t, h, "git-worker-site")
	path := "/v1/teams/" + a + "/projects"
	_, project := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	pid := project["id"].(string)
	if code, _ := request(t, h, http.MethodPost, "/v1/admin/teams/"+a+"/projects/"+pid+"/github-binding", "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 200 {
		t.Fatalf("binding: %d", code)
	}
	body := []byte(`{"installation":{"id":42},"repository":{"id":1001},"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `"}`)
	if code, _ := gitEvent(t, h, "auto-worker-delivery", body, true); code != 202 {
		t.Fatalf("signed webhook: %d", code)
	}
	worker := pipeline.NewProcessor(db, sources, artifacts, client, "preview.test", "sites.test")
	if did, err := worker.ProcessOne(context.Background()); !did || err != nil {
		t.Fatalf("automatic fixed Git source: %v %v", did, err)
	}
	statusURL := path + "/" + pid + "/github-pushes/auto-worker-delivery"
	code, status := request(t, h, http.MethodGet, statusURL, owner, "", nil)
	if code != 200 || status["status"] != "preview_candidate" || status["deploymentId"] == nil {
		t.Fatalf("Git candidate status: %d %v", code, status)
	}
	depID := status["deploymentId"].(string)
	var host, digest string
	if err := db.QueryRow(context.Background(), `SELECT desired_host,bundle_digest FROM site_deployments WHERE id=$1`, depID).Scan(&host, &digest); err != nil {
		t.Fatal(err)
	}
	live := routing.NewLiveGateway(routing.NewDurableRegistry(db, artifacts), nil)
	if err := live.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	live.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil))
	if w.Code != 401 {
		t.Fatalf("automatic Preview opened anonymously: %d", w.Code)
	}
	if did, err := worker.PromoteOne(context.Background()); did || err != nil {
		t.Fatalf("Git push promoted before private preview observed: %v %v", did, err)
	}
	if err := routing.NewDurableRegistry(db, artifacts).MarkServed(context.Background(), routing.Route{TeamID: a, ProjectID: pid, DeploymentID: depID, Generation: 1, Private: true}, host, digest); err != nil {
		t.Fatal(err)
	}
	if did, err := worker.PromoteOne(context.Background()); !did || err != nil {
		t.Fatalf("latest main SHA not promoted after preview: %v %v", did, err)
	}
	if code, result := request(t, h, http.MethodGet, path+"/"+pid+"/aliases/production", owner, "", nil); code != 200 || result["state"] != "requested" || result["observedGeneration"] != float64(0) {
		t.Fatalf("unobserved production reported active: %d %v", code, result)
	}
}
