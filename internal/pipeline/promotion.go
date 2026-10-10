package pipeline

import (
	"context"
	"errors"
	"strings"

	"elastic-web-platform/internal/routing"

	"github.com/jackc/pgx/v5"
)

type readyPush struct {
	delivery, team, project, sha, branch, deploymentID string
	installationID, repositoryID                       int64
}

// PromoteOne processes only the newest still-current production branch push
// whose private Preview has actually been served by the gateway. It writes a
// desired Alias generation; public ACTIVE requires a separate HTTPS readback.
func (p *Processor) PromoteOne(ctx context.Context) (bool, error) {
	return p.promoteOne(ctx, "")
}

// PromoteOneActionsPilot never promotes old App/webhook fixtures while the
// passwordless A/B trial is running. As before, a real served Preview and the
// *current* branch head are required before a production intent can be set.
func (p *Processor) PromoteOneActionsPilot(ctx context.Context) (bool, error) {
	return p.promoteOne(ctx, "actions_oidc")
}

func (p *Processor) promoteOne(ctx context.Context, origin string) (bool, error) {
	if p.productionSuffix == "" {
		return false, nil
	}
	if len(p.productionSuffix) > 180 || !suffixPattern.MatchString(p.productionSuffix) || strings.Contains(p.productionSuffix, "..") {
		return false, errors.New("invalid production suffix")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var item readyPush
	err = tx.QueryRow(ctx, `SELECT p.delivery_id,p.team_id,p.project_id,p.commit_sha,b.default_branch,d.installation_id,d.repository_id,site.id
		FROM github_pushes p JOIN github_deliveries d ON d.delivery_id=p.delivery_id
		JOIN github_bindings b ON b.team_id=p.team_id AND b.project_id=p.project_id AND b.installation_id=d.installation_id AND b.repository_id=d.repository_id AND b.status='verified'
		JOIN github_verified_sources src ON src.delivery_id=p.delivery_id AND src.team_id=p.team_id AND src.project_id=p.project_id
		JOIN site_deployments site ON site.team_id=p.team_id AND site.project_id=p.project_id AND site.source_kind='github' AND site.source_id=src.id AND site.observed_state='preview_ready'
		WHERE p.status='preview_candidate' AND p.next_attempt_at<=now() AND p.ref='refs/heads/'||b.default_branch
		AND ($1::text='' OR d.origin=$1::text)
		ORDER BY d.received_at,p.delivery_id LIMIT 1 FOR UPDATE OF p SKIP LOCKED`, origin).Scan(&item.delivery, &item.team, &item.project, &item.sha, &item.branch, &item.installationID, &item.repositoryID, &item.deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var later bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM github_pushes newer JOIN github_deliveries n ON n.delivery_id=newer.delivery_id
		JOIN github_deliveries old ON old.delivery_id=$1
		WHERE newer.team_id=$2 AND newer.project_id=$3 AND newer.ref='refs/heads/'||$4
		AND (n.received_at,n.delivery_id)>(old.received_at,old.delivery_id))`, item.delivery, item.team, item.project, item.branch).Scan(&later)
	if err != nil {
		return true, err
	}
	if later {
		return true, commitPromotionStatus(ctx, tx, item, "superseded")
	}
	head, err := p.fetcher.CurrentBranchHead(ctx, item.installationID, item.repositoryID, item.branch)
	if err != nil {
		if origin == "actions_oidc" {
			// A short-lived Actions token is never persisted. GitHub public API
			// throttling is a deferred verification, not a failed deployment.
			_, updateErr := tx.Exec(ctx, `UPDATE github_pushes SET next_attempt_at=now()+interval '2 minutes',last_error_code='branch_head_unavailable'
				WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, item.delivery, item.team, item.project)
			if updateErr != nil {
				return true, updateErr
			}
			return true, tx.Commit(ctx)
		}
		_, updateErr := tx.Exec(ctx, `UPDATE github_pushes SET attempts=attempts+1,
			status=CASE WHEN attempts+1>=5 THEN 'failed' ELSE 'preview_candidate' END,
			next_attempt_at=now()+interval '5 seconds',last_error_code='branch_head_unavailable'
			WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, item.delivery, item.team, item.project)
		if updateErr != nil {
			return true, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return true, commitErr
		}
		return true, errors.New("current Git production ref could not be verified")
	}
	if head != item.sha {
		return true, commitPromotionStatus(ctx, tx, item, "superseded")
	}
	host := "p-" + strings.TrimPrefix(item.project, "p_") + "." + p.productionSuffix
	var currentID string
	var generation int64
	err = tx.QueryRow(ctx, `SELECT deployment_id,generation FROM site_hosts WHERE hostname=$1 AND team_id=$2 AND project_id=$3 AND NOT is_fixed`, host, item.team, item.project).Scan(&currentID, &generation)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return true, err
	}
	if currentID != item.deploymentID {
		if _, err := routing.NewDurableRegistry(p.db, p.artifacts).SwapAlias(ctx, host, item.team, item.project, generation, item.deploymentID, false); err != nil {
			return true, err
		}
	}
	return true, commitPromotionStatus(ctx, tx, item, "production_requested")
}

func commitPromotionStatus(ctx context.Context, tx pgx.Tx, item readyPush, status string) error {
	if _, err := tx.Exec(ctx, `UPDATE github_pushes SET status=$4,last_error_code='' WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, item.delivery, item.team, item.project, status); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,$2,$3)`, item.team, "github."+status, item.deploymentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
