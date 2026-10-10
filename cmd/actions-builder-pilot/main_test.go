package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/db/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerOnlySelectsApprovedOIDCJobsAndSkipsIssuedOrders(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set an isolated PostgreSQL TEST_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("actions_builder_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	team, project := "t_f9cf963718a72612c48940af8a8296e4", "p_34401f5c05af1079010cb95ee3ec1275"
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES($1,'pilot','Pilot')`, team); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO projects(id,team_id,slug,name) VALUES($2,$1,'site','Site')`, team, project); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status) VALUES($1,$2,12,1404606559,'usiboy/testA','main','verified')`, team, project); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"webhook", "actions_oidc"} {
		delivery := "delivery-" + origin
		sha := strings.Repeat("a", 40)
		if _, err := db.Exec(ctx, `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status,origin) VALUES($1,'\x00',12,1404606559,'refs/heads/main',$2,'pending_commit_verification',$3)`, delivery, sha, origin); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status) VALUES($1,$2,$3,$4,'refs/heads/main','awaiting_isolated_build')`, delivery, team, project, sha); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO github_verified_sources(id,team_id,project_id,delivery_id,commit_sha,tree_sha,archive_sha256,size_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,123)`, "gsrc_"+origin, team, project, delivery, sha, strings.Repeat("b", 40), strings.Repeat("c", 64)); err != nil {
			t.Fatal(err)
		}
	}
	item, err := pending(ctx, db)
	if err != nil || item.delivery != "delivery-actions_oidc" || item.name != "testA" || item.plan.CPUMilli != 1000 {
		t.Fatalf("wrong pending Builder job: %+v %v", item, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_build_jobs(team_id,project_id,delivery_id,source_id,job_id,nonce_hash,version,builder_id,image_digest,cpu_milli,memory_mib,storage_mib,status,issued_at,expires_at)
		VALUES($1,$2,'delivery-actions_oidc','gsrc_actions_oidc',$3,$4,1,$5,$6,1000,2048,2048,'issued',now(),now()+interval '15 minutes')`, team, project, "job_"+strings.Repeat("a", 32), make([]byte, 32), builderID, imageDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := pending(ctx, db); err != pgx.ErrNoRows {
		t.Fatalf("issued order was claimed again: %v", err)
	}
}
