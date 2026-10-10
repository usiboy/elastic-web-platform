package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/routing"
	"elastic-web-platform/internal/source/uploads"
)

func TestPromotionNeedsObservedPreviewAndOnlyObservedProductionBecomesActive(t *testing.T) {
	db := testDatabase(t)
	artifacts := bundle.NewDiskStore(t.TempDir())
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(t.TempDir())), WithArtifactStore(artifacts), WithPreviewSuffix("preview.test"), WithProductionSuffix("sites.test"))
	a, owner := createTeam(t, h, "promote-team")
	b, other := createTeam(t, h, "other-promote")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	id := p["id"].(string)
	_, source := uploadSource(t, h, path+"/"+id+"/sources/uploads", owner, "source", deploymentSource(t, map[string]string{"index.html": "<h1>Ready</h1>"}))
	_, candidate := request(t, h, http.MethodPost, path+"/"+id+"/deployments", owner, "deploy", map[string]string{"sourceId": source["sourceId"].(string)})
	depID := candidate["deploymentId"].(string)
	endpoint := path + "/" + id + "/aliases/production:promote"
	requestBody := map[string]any{"deploymentId": depID, "expectedGeneration": 0}
	if code, _ := request(t, h, http.MethodPost, endpoint, owner, "", requestBody); code != 409 {
		t.Fatalf("unobserved preview promoted: %d", code)
	}
	_, viewer := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "viewer", map[string]string{"role": "viewer"})
	if code, _ := request(t, h, http.MethodPost, endpoint, viewer["token"].(string), "", requestBody); code != 403 {
		t.Fatalf("viewer promoted: %d", code)
	}
	if code, _ := request(t, h, http.MethodPost, "/v1/teams/"+b+"/projects/"+id+"/aliases/production:promote", other, "", requestBody); code != 404 {
		t.Fatalf("other Team promoted: %d", code)
	}
	var digest string
	if err := db.QueryRow(context.Background(), `SELECT bundle_digest FROM site_deployments WHERE id=$1`, depID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	control := routing.NewDurableRegistry(db, artifacts)
	preview := routing.Route{TeamID: a, ProjectID: id, DeploymentID: depID, Generation: 1, Private: true}
	if err := control.MarkServed(context.Background(), preview, candidate["desiredHost"].(string), digest); err != nil {
		t.Fatal(err)
	}
	code, result := request(t, h, http.MethodPost, endpoint, owner, "", requestBody)
	if code != 202 || result["desiredGeneration"] != float64(1) || result["observedGeneration"] != float64(0) || !strings.HasSuffix(result["desiredHost"].(string), ".sites.test") {
		t.Fatalf("promotion intent: %d %v", code, result)
	}
	code, newer := request(t, h, http.MethodPost, endpoint, owner, "", map[string]any{"deploymentId": depID, "expectedGeneration": 1})
	if code != 202 || newer["desiredGeneration"] != float64(2) || newer["observedGeneration"] != float64(0) {
		t.Fatalf("unobserved first generation misreported as active: %d %v", code, newer)
	}
	if code, _ := request(t, h, http.MethodPost, endpoint, owner, "", requestBody); code != 409 {
		t.Fatalf("stale generation accepted: %d", code)
	}
	aliasPath := path + "/" + id + "/aliases/production"
	if code, alias := request(t, h, http.MethodGet, aliasPath, owner, "", nil); code != 200 || alias["state"] != "requested" {
		t.Fatalf("unobserved alias active: %d %v", code, alias)
	}
	if code, project := request(t, h, http.MethodGet, path+"/"+id, owner, "", nil); code != 200 || project["status"] != "not_deployed" {
		t.Fatalf("unserved project active: %d %v", code, project)
	}
	if err := control.MarkServed(context.Background(), routing.Route{TeamID: a, ProjectID: id, DeploymentID: depID, Generation: 2, Private: false}, result["desiredHost"].(string), digest); err != nil {
		t.Fatal(err)
	}
	if code, alias := request(t, h, http.MethodGet, aliasPath, owner, "", nil); code != 200 || alias["state"] != "active" || alias["observedGeneration"] != float64(2) {
		t.Fatalf("observed alias: %d %v", code, alias)
	}
}
