package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var polledCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// PollOne obtains a fixed branch SHA through the same single-repository App
// identity as the webhook worker. The persisted origin explicitly says
// app_poll: an App API observation is never misrepresented as a signed push.
// This allows private, operator-controlled tests without public DNS/webhooks.
func (p *Processor) PollOne(ctx context.Context) (bool, error) {
	if p.db == nil || p.fetcher == nil {
		return false, errors.New("Git branch polling is not configured")
	}
	rows, err := p.db.Query(ctx, `SELECT team_id,project_id,installation_id,repository_id,default_branch
		FROM github_bindings WHERE status='verified' ORDER BY team_id,project_id LIMIT 1001`)
	if err != nil {
		return false, err
	}
	type target struct {
		team, project, branch    string
		installation, repository int64
	}
	var targets []target
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.team, &item.project, &item.installation, &item.repository, &item.branch); err != nil {
			rows.Close()
			return false, err
		}
		targets = append(targets, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(targets) > 1000 {
		return false, errors.New("too many Git bindings for one polling cycle")
	}
	for _, item := range targets {
		sha, err := p.fetcher.CurrentBranchHead(ctx, item.installation, item.repository, item.branch)
		if err != nil {
			return false, err
		}
		if !polledCommitPattern.MatchString(sha) {
			return false, errors.New("Git branch head is not a full commit SHA")
		}
		ref := "refs/heads/" + item.branch
		hash := sha256.Sum256([]byte(fmt.Sprintf("app-poll:v1:%s:%s:%d:%d:%s:%s", item.team, item.project, item.installation, item.repository, ref, sha)))
		delivery := "poll-" + hex.EncodeToString(hash[:16])
		tx, err := p.db.Begin(ctx)
		if err != nil {
			return false, err
		}
		var locked int64
		err = tx.QueryRow(ctx, `SELECT repository_id FROM github_bindings WHERE team_id=$1 AND project_id=$2 AND installation_id=$3 AND repository_id=$4 AND status='verified' FOR SHARE`, item.team, item.project, item.installation, item.repository).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			continue
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return false, err
		}
		var prior []byte
		var origin string
		err = tx.QueryRow(ctx, `SELECT payload_hash,origin FROM github_deliveries WHERE delivery_id=$1`, delivery).Scan(&prior, &origin)
		if err == nil {
			_ = tx.Rollback(ctx)
			if origin != "app_poll" || !sameHash(prior, hash) {
				return false, errors.New("Git poll event identity conflict")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return false, err
		}
		inserted, insertErr := tx.Exec(ctx, `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status,origin)
			VALUES($1,$2,$3,$4,$5,$6,'pending_commit_verification','app_poll') ON CONFLICT DO NOTHING`, delivery, hash[:], item.installation, item.repository, ref, sha)
		if insertErr != nil {
			_ = tx.Rollback(ctx)
			return false, insertErr
		}
		if inserted.RowsAffected() == 0 {
			var committed []byte
			var committedOrigin string
			checkErr := tx.QueryRow(ctx, `SELECT payload_hash,origin FROM github_deliveries WHERE delivery_id=$1`, delivery).Scan(&committed, &committedOrigin)
			_ = tx.Rollback(ctx)
			if checkErr != nil || committedOrigin != "app_poll" || !sameHash(committed, hash) {
				return false, errors.New("conflicting Git poll event")
			}
			continue
		}
		_, err = tx.Exec(ctx, `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status)
			VALUES($1,$2,$3,$4,$5,'pending_commit_verification') ON CONFLICT DO NOTHING`, delivery, item.team, item.project, sha, ref)
		if err != nil {
			_ = tx.Rollback(ctx)
			return false, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.branch_polled',$2)`, item.team, delivery); err != nil {
			_ = tx.Rollback(ctx)
			return false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func sameHash(old []byte, current [32]byte) bool {
	if len(old) != len(current) {
		return false
	}
	for i := range old {
		if old[i] != current[i] {
			return false
		}
	}
	return true
}
