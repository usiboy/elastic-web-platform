// operator-git-binding 仅为用户明确授权的 testA/testB 固定 Project 写入 Git 来源。
// 短期单仓库令牌由 Mac 签发，CLI 从 ECS 受限文件读取，不对租户 HTTP 暴露。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/source/github"

	"github.com/jackc/pgx/v5/pgxpool"
)

const installation = int64(167883648)

type target struct {
	team, project, name string
	repoID              int64
}

var targets = []target{
	{"t_f9cf963718a72612c48940af8a8296e4", "p_34401f5c05af1079010cb95ee3ec1275", "usiboy/testA", 1404606559},
	{"t_eee8046ca49c271f5bea06acf285be66", "p_e5123bae12388454bb82fc1590750ab9", "usiboy/testB", 1404606859},
}

func main() {
	if os.Getenv("DATABASE_URL") == "" {
		log.Fatal("operator binding requires the protected new-platform database URL")
	}
	file, err := github.LoadScopedTokenFile(os.Getenv("EWP_GITHUB_SCOPED_TOKEN_FILE"), installation, targets[0].repoID, targets[1].repoID)
	if err != nil {
		log.Fatal("operator Git token scope rejected: ", err)
	}
	client, err := github.NewScopedTokenClient(installation, file.Credentials, "https://api.github.com", nil)
	if err != nil {
		log.Fatal("single-repository credentials rejected: ", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, item := range targets {
		repo, err := client.VerifyRepository(ctx, installation, item.repoID)
		if err != nil || repo.ID != item.repoID || repo.FullName != item.name || repo.DefaultBranch != "main" {
			log.Fatal("fixed GitHub repository identity did not match the approved project")
		}
	}
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("new-platform database unavailable")
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		log.Fatal("new-platform database migration failed: ", err)
	}
	for _, item := range targets {
		tx, err := db.Begin(ctx)
		if err != nil {
			log.Fatal("binding transaction unavailable")
		}
		var teamID, projectID string
		if err := tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, item.team).Scan(&teamID); err != nil {
			_ = tx.Rollback(ctx)
			log.Fatal("approved Team not found")
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE team_id=$1 AND id=$2`, item.team, item.project).Scan(&projectID); err != nil {
			_ = tx.Rollback(ctx)
			log.Fatal("approved Project not found")
		}
		inserted, err := tx.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status,verified_at)
			VALUES($1,$2,$3,$4,$5,'main','verified',now()) ON CONFLICT DO NOTHING`, item.team, item.project, installation, item.repoID, item.name)
		if err != nil {
			_ = tx.Rollback(ctx)
			log.Fatal("fixed Git binding insert failed")
		}
		var installationID, repoID int64
		var fullName, branch, status string
		err = tx.QueryRow(ctx, `SELECT installation_id,repository_id,full_name,default_branch,status FROM github_bindings WHERE team_id=$1 AND project_id=$2`, item.team, item.project).Scan(&installationID, &repoID, &fullName, &branch, &status)
		if err != nil || installationID != installation || repoID != item.repoID || fullName != item.name || branch != "main" || status != "verified" {
			_ = tx.Rollback(ctx)
			log.Fatal("existing binding differs; refusing to overwrite tenant work")
		}
		if inserted.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.operator_scoped_binding',$2)`, item.team, item.project); err != nil {
				_ = tx.Rollback(ctx)
				log.Fatal("Git binding audit failed")
			}
		}
		if err := tx.Commit(ctx); err != nil {
			log.Fatal("Git binding transaction failed")
		}
		fmt.Printf("repo_bound=%d team=%s project=%s\n", item.repoID, item.team, item.project)
	}
}
