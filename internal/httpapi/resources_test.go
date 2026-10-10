package httpapi

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"elastic-web-platform/db/migrations"
)

func TestProjectResourceReservationsRejectOvercommitAcrossProjectsAndRestart(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "capacity-a")
	b, otherOwner := createTeam(t, h, "capacity-b")
	path := "/v1/teams/" + a
	otherPath := "/v1/teams/" + b
	_, first := request(t, h, http.MethodPost, path+"/projects", owner, "site-a", map[string]string{"slug": "site-a", "name": "Site A"})
	_, second := request(t, h, http.MethodPost, path+"/projects", owner, "site-b", map[string]string{"slug": "site-b", "name": "Site B"})
	firstPath := path + "/projects/" + first["id"].(string) + "/resources"
	secondPath := path + "/projects/" + second["id"].(string) + "/resources"
	budgetPath := "/v1/admin/teams/" + a + "/resource-budget"
	budget := map[string]any{"expectedVersion": 0, "cpuMilli": 1000, "memoryMiB": 2048, "storageMiB": 1024}
	if code, result := request(t, h, http.MethodPut, budgetPath, "bootstrap-local-test", "", budget); code != 201 || result["version"] != float64(1) {
		t.Fatalf("bootstrap budget: %d %v", code, result)
	}
	if code, _ := request(t, h, http.MethodPut, budgetPath, owner, "", budget); code != 401 {
		t.Fatalf("Owner changed operator budget: %d", code)
	}
	reservation := map[string]any{"expectedVersion": 0, "cpuMilli": 600, "memoryMiB": 1024, "storageMiB": 640}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, endpoint := range []string{firstPath, secondPath} {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()
			code, _ := request(t, h, http.MethodPut, endpoint, owner, "", reservation)
			results <- code
		}(endpoint)
	}
	wg.Wait()
	close(results)
	created, rejected := 0, 0
	for code := range results {
		if code == 201 {
			created++
		} else if code == 409 {
			rejected++
		} else {
			t.Fatalf("unexpected reservation response: %d", code)
		}
	}
	if created != 1 || rejected != 1 {
		t.Fatalf("concurrent budget oversubscribed: created=%d rejected=%d", created, rejected)
	}
	if err := migrations.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	h = NewServer(db, "bootstrap-local-test")
	code, summary := request(t, h, http.MethodGet, path+"/resources", owner, "", nil)
	if code != 200 || summary["reserved"].(map[string]any)["cpuMilli"] != float64(600) || summary["available"].(map[string]any)["storageMiB"] != float64(384) {
		t.Fatalf("readback after restart: %d %v", code, summary)
	}
	if code, _ := request(t, h, http.MethodGet, path+"/resources", otherOwner, "", nil); code != 404 {
		t.Fatalf("cross-Team budget visible: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, otherPath+"/projects/"+first["id"].(string)+"/resources", otherOwner, "", nil); code != 404 {
		t.Fatalf("cross-Team project reservation visible: %d", code)
	}
	if code, _ := request(t, h, http.MethodPut, budgetPath, "bootstrap-local-test", "", map[string]any{"expectedVersion": 1, "cpuMilli": 500, "memoryMiB": 2048, "storageMiB": 1024}); code != 409 {
		t.Fatalf("operator shrank below committed reservations: %d", code)
	}
}

func TestProjectResourceReservationsAreOwnerOnlyAndCASProtected(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	team, owner := createTeam(t, h, "capacity-scope")
	path := "/v1/teams/" + team
	_, project := request(t, h, http.MethodPost, path+"/projects", owner, "project", map[string]string{"slug": "site", "name": "Site"})
	endpoint := path + "/projects/" + project["id"].(string) + "/resources"
	if code, _ := request(t, h, http.MethodGet, endpoint, owner, "", nil); code != 200 {
		t.Fatalf("unallocated project not visible: %d", code)
	}
	request(t, h, http.MethodPut, "/v1/admin/teams/"+team+"/resource-budget", "bootstrap-local-test", "", map[string]any{"expectedVersion": 0, "cpuMilli": 1500, "memoryMiB": 2048, "storageMiB": 1024})
	_, viewer := request(t, h, http.MethodPost, path+"/tokens", owner, "viewer", map[string]any{"role": "viewer"})
	_, agent := request(t, h, http.MethodPost, path+"/tokens", owner, "agent", map[string]any{"role": "agent", "projectId": project["id"], "actions": []string{"projects:read"}})
	allocation := map[string]any{"expectedVersion": 0, "cpuMilli": 500, "memoryMiB": 512, "storageMiB": 256}
	if code, _ := request(t, h, http.MethodPut, endpoint, viewer["token"].(string), "", allocation); code != 403 {
		t.Fatalf("viewer assigned resources: %d", code)
	}
	if code, _ := request(t, h, http.MethodPut, endpoint, agent["token"].(string), "", allocation); code != 403 {
		t.Fatalf("agent assigned resources: %d", code)
	}
	if code, result := request(t, h, http.MethodPut, endpoint, owner, "", allocation); code != 201 || result["state"] != "reserved_not_provisioned" {
		t.Fatalf("initial reservation: %d %v", code, result)
	}
	if code, _ := request(t, h, http.MethodPut, endpoint, owner, "", allocation); code != 409 {
		t.Fatalf("stale CAS changed reservation: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, endpoint, viewer["token"].(string), "", nil); code != 200 {
		t.Fatalf("viewer cannot read project allocation: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, endpoint, agent["token"].(string), "", nil); code != 200 {
		t.Fatalf("scoped agent cannot read own allocation: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, path+"/resources", agent["token"].(string), "", nil); code != 403 {
		t.Fatalf("agent saw team-wide budget: %d", code)
	}
	if code, _ := request(t, h, http.MethodPut, endpoint, owner, "", map[string]any{"expectedVersion": 1, "cpuMilli": -1, "memoryMiB": 512, "storageMiB": 256}); code != 422 {
		t.Fatalf("negative resource accepted: %d", code)
	}
	if code, _ := request(t, h, http.MethodPut, endpoint, owner, "", map[string]any{"expectedVersion": 1, "cpuMilli": 0, "memoryMiB": 0, "storageMiB": 0}); code != 200 {
		t.Fatalf("explicit release failed: %d", code)
	}
	if code, result := request(t, h, http.MethodGet, path+"/resources", owner, "", nil); code != 200 || result["reserved"].(map[string]any)["cpuMilli"] != float64(0) {
		t.Fatalf("released budget not recovered: %d %v", code, result)
	}
}

func TestTeamBudgetChangeAndProjectReservationShareCapacityLock(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	team, owner := createTeam(t, h, "capacity-race")
	path := "/v1/teams/" + team
	_, project := request(t, h, http.MethodPost, path+"/projects", owner, "site", map[string]string{"slug": "site", "name": "Site"})
	budgetPath := "/v1/admin/teams/" + team + "/resource-budget"
	projectPath := path + "/projects/" + project["id"].(string) + "/resources"
	if status, _ := request(t, h, http.MethodGet, path+"/resources", owner, "", nil); status != 404 {
		t.Fatalf("unbudgeted Team had an allocation: %d", status)
	}
	initial := map[string]any{"expectedVersion": 0, "cpuMilli": 1000, "memoryMiB": 1024, "storageMiB": 1024}
	if status, _ := request(t, h, http.MethodPut, projectPath, owner, "", initial); status != 409 {
		t.Fatalf("reserved project without budget: %d", status)
	}
	if status, _ := request(t, h, http.MethodPut, budgetPath, "bootstrap-local-test", "", initial); status != 201 {
		t.Fatalf("initial Team budget: %d", status)
	}

	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, action := range []func(){
		func() {
			code, _ := request(t, h, http.MethodPut, budgetPath, "bootstrap-local-test", "", map[string]any{"expectedVersion": 1, "cpuMilli": 500, "memoryMiB": 512, "storageMiB": 512})
			results <- code
		},
		func() {
			code, _ := request(t, h, http.MethodPut, projectPath, owner, "", map[string]any{"expectedVersion": 0, "cpuMilli": 600, "memoryMiB": 640, "storageMiB": 640})
			results <- code
		},
	} {
		wg.Add(1)
		go func(action func()) { defer wg.Done(); action() }(action)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for code := range results {
		if code == 200 || code == 201 {
			success++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatalf("unexpected budget/reservation race: %d", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("capacity lock did not serialize budget and reservation: success=%d conflict=%d", success, conflict)
	}
	_, summary := request(t, h, http.MethodGet, path+"/resources", owner, "", nil)
	budget := summary["budget"].(map[string]any)["cpuMilli"].(float64)
	reserved := summary["reserved"].(map[string]any)["cpuMilli"].(float64)
	if reserved > budget || (budget == 500 && reserved != 0) || (budget == 1000 && reserved != 600) {
		t.Fatalf("capacity drift after concurrent writes: budget=%v reserved=%v", budget, reserved)
	}
	if reserved == 600 {
		if code, _ := request(t, h, http.MethodPut, projectPath, owner, "", map[string]any{"expectedVersion": 1, "cpuMilli": 0, "memoryMiB": 0, "storageMiB": 0}); code != 200 {
			t.Fatalf("release did not restore Team capacity: %d", code)
		}
		if code, _ := request(t, h, http.MethodPut, budgetPath, "bootstrap-local-test", "", map[string]any{"expectedVersion": 1, "cpuMilli": 500, "memoryMiB": 512, "storageMiB": 512}); code != 200 {
			t.Fatalf("operator could not shrink released budget: %d", code)
		}
	}
}
