CREATE TABLE IF NOT EXISTS github_oauth_states (
    state_hash bytea PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    actor_token_id text NOT NULL REFERENCES access_tokens(id),
    installation_id bigint NOT NULL CHECK (installation_id>0),
    repository_id bigint NOT NULL CHECK (repository_id>0),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id,project_id) REFERENCES projects(team_id,id)
);

CREATE INDEX IF NOT EXISTS github_oauth_states_expiry ON github_oauth_states(expires_at);
