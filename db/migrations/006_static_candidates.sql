ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS source_kind text NOT NULL DEFAULT 'operator_fixture';
ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS source_id text;
ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS desired_host text;
ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS observed_state text NOT NULL DEFAULT 'not_deployed';

CREATE TABLE IF NOT EXISTS deployment_requests (
    team_id text NOT NULL,
    project_id text NOT NULL,
    request_key text NOT NULL,
    request_hash bytea NOT NULL,
    deployment_id text NOT NULL,
    PRIMARY KEY (team_id,project_id,request_key),
    FOREIGN KEY (team_id,project_id,deployment_id) REFERENCES site_deployments(team_id,project_id,id)
);
