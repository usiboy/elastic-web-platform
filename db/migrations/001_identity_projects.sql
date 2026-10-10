CREATE TABLE IF NOT EXISTS teams (
    id text PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS projects (
    id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(id),
    slug text NOT NULL,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'not_deployed',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, slug),
    UNIQUE (team_id, id)
);

CREATE TABLE IF NOT EXISTS access_tokens (
    id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(id),
    project_id text,
    token_hash bytea NOT NULL UNIQUE,
    role text NOT NULL CHECK (role IN ('owner', 'developer', 'viewer', 'agent')),
    actions text[] NOT NULL DEFAULT '{}',
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id, project_id) REFERENCES projects(team_id, id)
);

CREATE TABLE IF NOT EXISTS project_requests (
    team_id text NOT NULL REFERENCES teams(id),
    request_key text NOT NULL,
    request_hash bytea NOT NULL,
    project_id text NOT NULL,
    PRIMARY KEY (team_id, request_key),
    FOREIGN KEY (team_id, project_id) REFERENCES projects(team_id, id)
);

CREATE TABLE IF NOT EXISTS bootstrap_requests (
    request_key text PRIMARY KEY,
    request_hash bytea NOT NULL,
    team_id text NOT NULL REFERENCES teams(id)
);

CREATE TABLE IF NOT EXISTS token_requests (
    team_id text NOT NULL REFERENCES teams(id),
    request_key text NOT NULL,
    request_hash bytea NOT NULL,
    token_id text NOT NULL REFERENCES access_tokens(id),
    PRIMARY KEY (team_id, request_key)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id bigserial PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(id),
    actor_token_id text,
    action text NOT NULL,
    subject_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
