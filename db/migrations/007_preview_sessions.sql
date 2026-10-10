CREATE TABLE IF NOT EXISTS preview_exchanges (
    code_hash bytea PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    deployment_id text NOT NULL,
    actor_token_id text NOT NULL REFERENCES access_tokens(id),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id,project_id,deployment_id) REFERENCES site_deployments(team_id,project_id,id)
);

CREATE TABLE IF NOT EXISTS preview_sessions (
    session_hash bytea PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    deployment_id text NOT NULL,
    actor_token_id text NOT NULL REFERENCES access_tokens(id),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id,project_id,deployment_id) REFERENCES site_deployments(team_id,project_id,id)
);

CREATE INDEX IF NOT EXISTS preview_exchanges_expiry ON preview_exchanges(expires_at);
CREATE INDEX IF NOT EXISTS preview_sessions_expiry ON preview_sessions(expires_at);
