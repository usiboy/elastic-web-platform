CREATE TABLE IF NOT EXISTS site_deployments (
    id text PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    bundle_digest text NOT NULL CHECK (bundle_digest ~ '^[0-9a-f]{64}$'),
    source_sha256 text NOT NULL,
    status text NOT NULL DEFAULT 'candidate' CHECK (status='candidate'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id,project_id,id),
    FOREIGN KEY (team_id,project_id) REFERENCES projects(team_id,id)
);

CREATE TABLE IF NOT EXISTS site_hosts (
    hostname text PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    deployment_id text NOT NULL,
    generation bigint NOT NULL CHECK (generation>0),
    is_fixed boolean NOT NULL,
    is_private boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (team_id,project_id,deployment_id) REFERENCES site_deployments(team_id,project_id,id)
);

CREATE TABLE IF NOT EXISTS site_route_events (
    id bigserial PRIMARY KEY,
    hostname text NOT NULL,
    team_id text NOT NULL,
    project_id text NOT NULL,
    previous_deployment_id text,
    deployment_id text NOT NULL,
    generation bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (hostname,generation)
);
