package routing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// MarkServed records evidence of the exact bundle served by an HTTPS gateway.
// It never accepts a stale Alias generation or a different Host/digest.
func (r *DurableRegistry) MarkServed(ctx context.Context, route Route, host, digest string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var team, project, deployment, bundleDigest, state string
	var generation, observed int64
	var fixed, private bool
	err = tx.QueryRow(ctx, `SELECT h.team_id,h.project_id,h.deployment_id,h.generation,h.observed_generation,h.is_fixed,h.is_private,d.bundle_digest,d.observed_state
		FROM site_hosts h JOIN site_deployments d ON d.id=h.deployment_id AND d.team_id=h.team_id AND d.project_id=h.project_id
		WHERE h.hostname=$1 FOR UPDATE OF h`, host).Scan(&team, &project, &deployment, &generation, &observed, &fixed, &private, &bundleDigest, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("host is not routed")
	}
	if err != nil {
		return err
	}
	if team != route.TeamID || project != route.ProjectID || deployment != route.DeploymentID || generation != route.Generation || private != route.Private || bundleDigest != digest {
		return errors.New("served route does not match desired generation and bytes")
	}
	b, err := r.artifacts.Get(ctx, team, project, digest)
	if err != nil || b == nil || b.Verify() != nil {
		return errors.New("served artifact is not available")
	}
	if fixed && private {
		if state == "not_deployed" {
			if _, err = tx.Exec(ctx, `UPDATE site_deployments SET observed_state='preview_ready' WHERE id=$1 AND team_id=$2 AND project_id=$3 AND observed_state='not_deployed'`, deployment, team, project); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'preview.observed',$2)`, team, deployment); err != nil {
				return err
			}
		}
	} else if !fixed && !private {
		if state != "preview_ready" {
			return errors.New("production requires observed preview")
		}
		if observed < generation {
			if _, err = tx.Exec(ctx, `UPDATE site_hosts SET observed_generation=$2,observed_deployment_id=$3 WHERE hostname=$1`, host, generation, deployment); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE projects SET status='active' WHERE team_id=$1 AND id=$2`, team, project); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'production.observed',$2)`, team, deployment); err != nil {
				return err
			}
		}
	} else {
		return errors.New("unsupported route observation")
	}
	return tx.Commit(ctx)
}
