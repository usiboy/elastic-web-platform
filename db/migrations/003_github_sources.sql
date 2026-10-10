CREATE TABLE IF NOT EXISTS github_bindings (
    team_id text NOT NULL,
    project_id text NOT NULL,
    installation_id bigint NOT NULL CHECK (installation_id > 0),
    repository_id bigint NOT NULL CHECK (repository_id > 0),
    full_name text NOT NULL DEFAULT '',
    default_branch text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('pending','verified')),
    verified_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id,project_id),
    FOREIGN KEY (team_id,project_id) REFERENCES projects(team_id,id)
);

CREATE INDEX IF NOT EXISTS github_bindings_by_repository ON github_bindings(installation_id,repository_id) WHERE status='verified';

CREATE TABLE IF NOT EXISTS github_deliveries (
    delivery_id text PRIMARY KEY,
    payload_hash bytea NOT NULL,
    installation_id bigint NOT NULL,
    repository_id bigint NOT NULL,
    ref text NOT NULL,
    commit_sha text NOT NULL,
    status text NOT NULL CHECK (status IN ('ignored_unbound','pending_commit_verification')),
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS github_pushes (
    delivery_id text NOT NULL REFERENCES github_deliveries(delivery_id),
    team_id text NOT NULL,
    project_id text NOT NULL,
    commit_sha text NOT NULL,
    ref text NOT NULL,
    status text NOT NULL CHECK (status='pending_commit_verification'),
    PRIMARY KEY (delivery_id,team_id,project_id),
    FOREIGN KEY (team_id,project_id) REFERENCES projects(team_id,id)
);
