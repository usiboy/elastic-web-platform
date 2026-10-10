ALTER TABLE github_pushes DROP CONSTRAINT IF EXISTS github_pushes_status_check;
ALTER TABLE github_pushes ADD CONSTRAINT github_pushes_status_check
    CHECK (status IN ('pending_commit_verification','awaiting_isolated_build','preview_candidate','failed'));

ALTER TABLE github_pushes ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
ALTER TABLE github_pushes ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE github_pushes ADD COLUMN IF NOT EXISTS last_error_code text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS site_deployments_github_source_unique
    ON site_deployments(team_id,project_id,source_kind,source_id)
    WHERE source_kind='github' AND source_id IS NOT NULL;
