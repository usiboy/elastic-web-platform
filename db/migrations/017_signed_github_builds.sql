-- 签名构建交接记录：只允许固定 Git 来源的一个不可变制品，不代表已完成真实沙箱审计。
ALTER TABLE github_verified_sources ADD CONSTRAINT github_verified_sources_team_project_id UNIQUE(team_id, project_id, id);

CREATE TABLE github_build_attestations (
    team_id text NOT NULL,
    project_id text NOT NULL,
    delivery_id text NOT NULL,
    source_id text NOT NULL,
    deployment_id text NOT NULL,
    statement_sha256 text NOT NULL CHECK (statement_sha256 ~ '^[0-9a-f]{64}$'),
    public_key_sha256 text NOT NULL CHECK (public_key_sha256 ~ '^[0-9a-f]{64}$'),
    output_sha256 text NOT NULL CHECK (output_sha256 ~ '^[0-9a-f]{64}$'),
    builder_id text NOT NULL,
    builder_image_sha256 text NOT NULL CHECK (builder_image_sha256 ~ '^[0-9a-f]{64}$'),
    signature bytea NOT NULL CHECK (octet_length(signature) = 64),
    imported_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, project_id, delivery_id),
    UNIQUE (source_id),
    FOREIGN KEY (delivery_id, team_id, project_id) REFERENCES github_pushes(delivery_id, team_id, project_id),
    FOREIGN KEY (team_id, project_id, source_id) REFERENCES github_verified_sources(team_id, project_id, id),
    FOREIGN KEY (team_id, project_id, deployment_id) REFERENCES site_deployments(team_id, project_id, id)
);
