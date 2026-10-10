package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestGitPushListIsProjectScopedOrderedAndBounded(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "push-list-a")
	b, otherOwner := createTeam(t, h, "push-list-b")
	_, project := request(t, h, http.MethodPost, "/v1/teams/"+a+"/projects", owner, "a-project", map[string]string{"slug": "site", "name": "Site"})
	_, other := request(t, h, http.MethodPost, "/v1/teams/"+b+"/projects", otherOwner, "b-project", map[string]string{"slug": "site", "name": "Site"})
	path := "/v1/teams/" + a + "/projects/" + project["id"].(string) + "/github-pushes"
	if code, result := request(t, h, http.MethodGet, path, owner, "", nil); code != 200 || len(result["pushes"].([]any)) != 0 {
		t.Fatalf("empty project status: %d %v", code, result)
	}
	for index := 0; index < 22; index++ {
		id := fmt.Sprintf("delivery-%02d", index)
		_, err := db.Exec(context.Background(), `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status,received_at)
			VALUES($1,'\x00',42,1001,'refs/heads/main',$2,'pending_commit_verification','2026-10-06 00:00:00Z')`, id, strings.Repeat("a", 40))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(context.Background(), `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status)
			VALUES($1,$2,$3,$4,'refs/heads/main','awaiting_isolated_build')`, id, a, project["id"], strings.Repeat("a", 40)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.Exec(context.Background(), `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status)
		VALUES('other-delivery','\x00',42,1002,'refs/heads/main',$1,'pending_commit_verification')`, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status)
		VALUES('other-delivery',$1,$2,$3,'refs/heads/main','failed')`, b, other["id"], strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}
	code, first := request(t, h, http.MethodGet, path, owner, "", nil)
	if code != 200 || !first["hasMore"].(bool) || len(first["pushes"].([]any)) != 20 || first["pushes"].([]any)[0].(map[string]any)["deliveryId"] != "delivery-21" {
		t.Fatalf("first page did not reflect latest verified source order: %d %v", code, first)
	}
	cursor := first["nextCursor"].(string)
	code, second := request(t, h, http.MethodGet, path+"?cursor="+cursor, owner, "", nil)
	if code != 200 || second["hasMore"].(bool) || len(second["pushes"].([]any)) != 2 || second["pushes"].([]any)[0].(map[string]any)["deliveryId"] != "delivery-01" {
		t.Fatalf("next page skipped or crossed project: %d %v", code, second)
	}
	if code, _ := request(t, h, http.MethodGet, path+"?cursor=bogus", owner, "", nil); code != 422 {
		t.Fatalf("malformed cursor accepted: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, path, otherOwner, "", nil); code != 404 {
		t.Fatalf("cross-Team history exposed: %d", code)
	}
	_, scoped := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "scoped-agent", map[string]any{"role": "agent", "projectId": project["id"], "actions": []string{"projects:read"}})
	if code, result := request(t, h, http.MethodGet, path, scoped["token"].(string), "", nil); code != 200 || len(result["pushes"].([]any)) != 20 {
		t.Fatalf("project-scoped reader lost its own history: %d %v", code, result)
	}
	_, unscoped := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "unscoped-agent", map[string]any{"role": "agent", "projectId": project["id"], "actions": []string{"sources:read"}})
	if code, _ := request(t, h, http.MethodGet, path, unscoped["token"].(string), "", nil); code != 404 {
		t.Fatalf("agent lacking projects:read saw Git history: %d", code)
	}
}
