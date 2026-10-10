CREATE TABLE IF NOT EXISTS source_uploads (
    id text PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes integer NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 1048576),
    file_count integer NOT NULL CHECK (file_count BETWEEN 1 AND 128),
    status text NOT NULL DEFAULT 'stored_unverified' CHECK (status = 'stored_unverified'),
    actor_token_id text NOT NULL REFERENCES access_tokens(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id, project_id) REFERENCES projects(team_id, id),
    UNIQUE (team_id, project_id, id)
);

CREATE TABLE IF NOT EXISTS upload_requests (
    team_id text NOT NULL,
    project_id text NOT NULL,
    request_key text NOT NULL,
    request_hash bytea NOT NULL,
    source_id text NOT NULL,
    PRIMARY KEY (team_id, project_id, request_key),
    FOREIGN KEY (team_id, project_id, source_id) REFERENCES source_uploads(team_id, project_id, id)
);
