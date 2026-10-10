CREATE TABLE IF NOT EXISTS github_verified_sources (
    id text PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    delivery_id text NOT NULL,
    commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
    tree_sha text NOT NULL CHECK (tree_sha ~ '^[0-9a-f]{40}$'),
    archive_sha256 text NOT NULL CHECK (archive_sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes integer NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 1048576),
    status text NOT NULL DEFAULT 'verified_source' CHECK (status = 'verified_source'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id,project_id,delivery_id),
    FOREIGN KEY (delivery_id,team_id,project_id) REFERENCES github_pushes(delivery_id,team_id,project_id)
);
