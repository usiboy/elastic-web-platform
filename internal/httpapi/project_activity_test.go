package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestProjectActivityAndUsageAreMeasuredAndTenantScoped(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	teamA, ownerA := createTeam(t, h, "activity-a")
	teamB, ownerB := createTeam(t, h, "activity-b")
	_, a := request(t, h, http.MethodPost, "/v1/teams/"+teamA+"/projects", ownerA, "activity-a-project", map[string]string{"slug": "site", "name": "Site"})
	_, _ = request(t, h, http.MethodPost, "/v1/teams/"+teamB+"/projects", ownerB, "activity-b-project", map[string]string{"slug": "site", "name": "Site"})
	id := a["id"].(string)
	path := "/v1/teams/" + teamA + "/projects/" + id
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status)
		VALUES('activity-1','\x00',42,1001,'refs/heads/main',$1,'pending_commit_verification')`, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status)
		VALUES('activity-1',$1,$2,$3,'refs/heads/main','awaiting_isolated_build')`, teamA, id, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_verified_sources(id,team_id,project_id,delivery_id,commit_sha,tree_sha,archive_sha256,size_bytes)
		VALUES('gsrc_activity',$1,$2,'activity-1',$3,$4,$5,2048)`, teamA, id, strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.branch_polled','activity-1'),($2,'github.branch_polled','other-project')`, teamA, teamB); err != nil {
		t.Fatal(err)
	}
	code, usage := request(t, h, http.MethodGet, path+"/usage", ownerA, "", nil)
	if code != 200 || usage["sourceArchiveBytes"] != float64(2048) || usage["verifiedSources"] != float64(1) || usage["acceptedBuilds"] != float64(0) || usage["cpuTimeMs"] != nil {
		t.Fatalf("incorrect measured usage: %d %v", code, usage)
	}
	code, activity := request(t, h, http.MethodGet, path+"/activity", ownerA, "", nil)
	if code != 200 || len(activity["events"].([]any)) != 1 || activity["events"].([]any)[0].(map[string]any)["subjectId"] != "activity-1" {
		t.Fatalf("project audit leaked or missing: %d %v", code, activity)
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/teams/"+teamB+"/projects/"+id+"/activity", ownerB, "", nil); code != 404 {
		t.Fatalf("other Team accessed project audit: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/teams/"+teamB+"/projects/"+id+"/usage", ownerB, "", nil); code != 404 {
		t.Fatalf("other Team accessed project usage: %d", code)
	}
	_, agent := request(t, h, http.MethodPost, "/v1/teams/"+teamA+"/tokens", ownerA, "activity-agent", map[string]any{"role": "agent", "projectId": id, "actions": []string{"sources:read"}})
	if code, _ := request(t, h, http.MethodGet, path+"/activity", agent["token"].(string), "", nil); code != 404 {
		t.Fatalf("agent without projects:read accessed project audit: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, path+"/usage", agent["token"].(string), "", nil); code != 404 {
		t.Fatalf("agent without projects:read accessed project usage: %d", code)
	}
}
