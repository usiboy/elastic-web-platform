package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"elastic-web-platform/db/migrations"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to an isolated PostgreSQL instance")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("web_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func request(t *testing.T, handler http.Handler, method, path, token, key string, body any) (int, map[string]any) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s %s: HTTP %d invalid JSON: %s", method, path, w.Code, w.Body.String())
	}
	return w.Code, result
}

func createTeam(t *testing.T, h http.Handler, slug string) (string, string) {
	t.Helper()
	status, body := request(t, h, http.MethodPost, "/v1/teams", "bootstrap-local-test", slug, map[string]string{"slug": slug, "name": slug})
	if status != http.StatusCreated {
		t.Fatalf("create team: %d: %v", status, body)
	}
	return body["id"].(string), body["ownerToken"].(string)
}

func TestProjectsAreDurableIsolatedAndIdempotent(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, aToken := createTeam(t, h, "team-a")
	b, bToken := createTeam(t, h, "team-b")
	aPath := "/v1/teams/" + a + "/projects"
	bPath := "/v1/teams/" + b + "/projects"
	if code, _ := request(t, h, http.MethodPost, aPath, "", "new-a", map[string]string{"slug": "website", "name": "Site A"}); code != 401 {
		t.Fatalf("anonymous create: got %d", code)
	}
	code, first := request(t, h, http.MethodPost, aPath, aToken, "new-a", map[string]string{"slug": "website", "name": "Site A"})
	if code != 201 || first["status"] != "not_deployed" || first["id"] == "" {
		t.Fatalf("create A: %d %v", code, first)
	}
	code, repeated := request(t, h, http.MethodPost, aPath, aToken, "new-a", map[string]string{"slug": "website", "name": "Site A"})
	if code != 200 || repeated["id"] != first["id"] {
		t.Fatalf("idempotent retry: %d %v", code, repeated)
	}
	if code, _ := request(t, h, http.MethodPost, aPath, aToken, "new-a", map[string]string{"slug": "website", "name": "changed"}); code != 409 {
		t.Fatalf("same key different body: got %d", code)
	}
	if code, _ := request(t, h, http.MethodPost, bPath, bToken, "new-b", map[string]string{"slug": "website", "name": "Site B"}); code != 201 {
		t.Fatalf("same slug in B: got %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, bPath+"/"+first["id"].(string), aToken, "", nil); code != 404 {
		t.Fatalf("cross-team project: got %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, bPath, aToken, "", nil); code != 404 {
		t.Fatalf("cross-team list: got %d", code)
	}
	if code, _ := request(t, h, http.MethodPost, aPath, aToken, "", map[string]string{"slug": "other", "name": "Other"}); code != 422 {
		t.Fatalf("missing idempotency key: got %d", code)
	}
	h = NewServer(db, "bootstrap-local-test")
	code, list := request(t, h, http.MethodGet, aPath, aToken, "", nil)
	if code != 200 || len(list["projects"].([]any)) != 1 || list["projects"].([]any)[0].(map[string]any)["id"] != first["id"] {
		t.Fatalf("restart/list: %d %v", code, list)
	}
}

func TestAgentScopeViewerAndRevocation(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "team-a")
	b, _ := createTeam(t, h, "team-b")
	aPath := "/v1/teams/" + a + "/projects"
	_, project := request(t, h, http.MethodPost, aPath, owner, "site", map[string]string{"slug": "website", "name": "Website"})
	projectID := project["id"].(string)
	tokenPath := "/v1/teams/" + a + "/tokens"
	code, agent := request(t, h, http.MethodPost, tokenPath, owner, "agent", map[string]any{"role": "agent", "projectId": projectID, "actions": []string{"projects:read"}})
	if code != 201 || agent["token"] == "" {
		t.Fatalf("create agent: %d %v", code, agent)
	}
	agentToken := agent["token"].(string)
	if code, _ := request(t, h, http.MethodGet, aPath+"/"+projectID, agentToken, "", nil); code != 200 {
		t.Fatalf("agent own project: got %d", code)
	}
	if code, _ := request(t, h, http.MethodPost, aPath, agentToken, "unauthorized", map[string]string{"slug": "bad", "name": "Bad"}); code != 403 {
		t.Fatalf("agent create without grant: got %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/teams/"+b+"/projects", agentToken, "", nil); code != 404 {
		t.Fatalf("agent cross-team: got %d", code)
	}
	code, viewer := request(t, h, http.MethodPost, tokenPath, owner, "viewer", map[string]string{"role": "viewer"})
	if code != 201 {
		t.Fatalf("create viewer: %d %v", code, viewer)
	}
	if code, _ := request(t, h, http.MethodGet, aPath, viewer["token"].(string), "", nil); code != 200 {
		t.Fatalf("viewer list: got %d", code)
	}
	if code, _ := request(t, h, http.MethodPost, aPath, viewer["token"].(string), "viewer-write", map[string]string{"slug": "bad", "name": "Bad"}); code != 403 {
		t.Fatalf("viewer write: got %d", code)
	}
	if code, _ := request(t, h, http.MethodDelete, tokenPath+"/"+agent["id"].(string), owner, "", nil); code != 200 {
		t.Fatalf("revoke agent: got %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, aPath+"/"+projectID, agentToken, "", nil); code != 401 {
		t.Fatalf("revoked agent: got %d", code)
	}
	var stored string
	if err := db.QueryRow(context.Background(), "SELECT encode(token_hash, 'hex') FROM access_tokens WHERE id=$1", agent["id"].(string)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, agentToken) || len(stored) != 64 {
		t.Fatalf("token not stored as SHA-256 digest: %q", stored)
	}
}

func TestMeReturnsCurrentRoleAndScopeWithoutCredential(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "me-team")
	path := "/v1/teams/" + a + "/projects"
	_, project := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	_, agent := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "agent", map[string]any{"role": "agent", "projectId": project["id"], "actions": []string{"projects:read"}})
	code, me := request(t, h, http.MethodGet, "/v1/me", agent["token"].(string), "", nil)
	if code != 200 || me["teamId"] != a || me["role"] != "agent" || me["projectId"] != project["id"] || me["expiresAt"] == nil || me["token"] != nil {
		t.Fatalf("scoped identity: %d %v", code, me)
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/me", owner, "", nil); code != 200 {
		t.Fatalf("owner identity: %d", code)
	}
	if code, _ := request(t, h, http.MethodDelete, "/v1/teams/"+a+"/tokens/"+agent["id"].(string), owner, "", nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/me", agent["token"].(string), "", nil); code != 401 {
		t.Fatalf("revoked identity: %d", code)
	}
}

func TestConcurrentBootstrapRetryCreatesOnlyOneTeam(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/teams", strings.NewReader(`{"slug":"concurrent","name":"Concurrent"}`))
			req.Header.Set("Authorization", "Bearer bootstrap-local-test")
			req.Header.Set("Idempotency-Key", "same-request")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			results <- w.Code
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	for status := range results {
		if status == 201 {
			created++
		} else if status != 200 {
			t.Errorf("same-key retry got %d", status)
		}
	}
	if created != 1 {
		t.Fatalf("wanted one initial creation, got %d", created)
	}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM teams").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("created %d teams", count)
	}
}

func TestExpiredAgentAndRepeatedMigrations(t *testing.T) {
	db := testDatabase(t)
	if err := migrations.Apply(context.Background(), db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "expiry")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "first", map[string]string{"slug": "site", "name": "Site"})
	code, agent := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "agent", map[string]any{"role": "agent", "projectId": p["id"], "actions": []string{"projects:read"}})
	if code != 201 {
		t.Fatalf("agent creation: %d %v", code, agent)
	}
	if _, err := db.Exec(context.Background(), "UPDATE access_tokens SET expires_at=now()-interval '1 minute' WHERE id=$1", agent["id"].(string)); err != nil {
		t.Fatal(err)
	}
	if code, _ := request(t, h, http.MethodGet, path+"/"+p["id"].(string), agent["token"].(string), "", nil); code != 401 {
		t.Fatalf("expired agent: got %d", code)
	}
}

func TestMigrationLedgerRejectsChangedAppliedSQL(t *testing.T) {
	db := testDatabase(t)
	var versions int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil || versions != 22 {
		t.Fatalf("migration versions unavailable: %d %v", versions, err)
	}
	if _, err := db.Exec(context.Background(), "UPDATE schema_migrations SET sha256='different' WHERE version='001_identity_projects.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(context.Background(), db); err == nil {
		t.Fatal("modified applied migration was silently accepted")
	}
}

func TestProjectListCanReachBeyondFirstPage(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "many-projects")
	for i := 0; i < 101; i++ {
		if _, err := db.Exec(context.Background(), `INSERT INTO projects(id,team_id,slug,name) VALUES($1,$2,$3,$4)`, fmt.Sprintf("p_%032x", i), a, fmt.Sprintf("site-%03d", i), "Site"); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/teams/" + a + "/projects"
	code, first := request(t, h, http.MethodGet, path, owner, "", nil)
	if code != 200 || len(first["projects"].([]any)) != 100 || first["hasMore"] != true {
		t.Fatalf("first page: %d %v", code, first)
	}
	cursor, ok := first["nextCursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("no continuation for 101 projects: %v", first["nextCursor"])
	}
	code, second := request(t, h, http.MethodGet, path+"?cursor="+cursor, owner, "", nil)
	if code != 200 || len(second["projects"].([]any)) != 1 || second["hasMore"] != false {
		t.Fatalf("second page: %d %v", code, second)
	}
	if second["projects"].([]any)[0].(map[string]any)["id"] == first["projects"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("second page repeated the first")
	}
}
