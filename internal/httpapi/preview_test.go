package httpapi

import (
	"context"
	"crypto/sha256"
	"net/http"
	"testing"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/uploads"
)

func TestPreviewExchangeIsShortLivedScopedAndStoredOnlyAsHash(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(uploads.NewDiskStore(t.TempDir())), WithArtifactStore(bundle.NewDiskStore(t.TempDir())), WithPreviewSuffix("preview.test"))
	a, owner := createTeam(t, h, "preview-owner")
	b, other := createTeam(t, h, "preview-other")
	path := "/v1/teams/" + a + "/projects"
	_, project := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	id := project["id"].(string)
	_, source := uploadSource(t, h, path+"/"+id+"/sources/uploads", owner, "upload", deploymentSource(t, map[string]string{"index.html": "<h1>Private</h1>"}))
	_, candidate := request(t, h, http.MethodPost, path+"/"+id+"/deployments", owner, "deploy", map[string]string{"sourceId": source["sourceId"].(string)})
	endpoint := path + "/" + id + "/deployments/" + candidate["deploymentId"].(string) + "/preview-exchanges"
	if code, _ := request(t, h, http.MethodPost, "/v1/teams/"+b+"/projects/"+id+"/deployments/"+candidate["deploymentId"].(string)+"/preview-exchanges", other, "", nil); code != 404 {
		t.Fatalf("cross-team preview code: %d", code)
	}
	code, issued := request(t, h, http.MethodPost, endpoint, owner, "", nil)
	if code != 201 || issued["code"] == "" || issued["desiredHost"] != candidate["desiredHost"] {
		t.Fatalf("issue preview code: %d %v", code, issued)
	}
	digest := sha256.Sum256([]byte(issued["code"].(string)))
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM preview_exchanges WHERE code_hash=$1 AND expires_at>now() AND consumed_at IS NULL`, digest[:]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("exchange code not scoped/hashed: %d %v", count, err)
	}
	var stored string
	if err := db.QueryRow(context.Background(), `SELECT encode(code_hash,'hex') FROM preview_exchanges`).Scan(&stored); err != nil || stored == issued["code"] {
		t.Fatalf("cleartext preview code stored: %q %v", stored, err)
	}
}
