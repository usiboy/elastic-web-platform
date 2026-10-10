ALTER TABLE github_pushes DROP CONSTRAINT IF EXISTS github_pushes_status_check;
ALTER TABLE github_pushes ADD CONSTRAINT github_pushes_status_check
    CHECK (status IN ('pending_commit_verification','awaiting_isolated_build','preview_candidate','production_requested','superseded','failed'));
