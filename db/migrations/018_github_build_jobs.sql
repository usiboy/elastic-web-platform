-- 一次性控制面作业租约；只存随机 nonce 的哈希，不向网站/Agent 暴露。
CREATE TABLE github_build_jobs (
    team_id text NOT NULL,
    project_id text NOT NULL,
    delivery_id text NOT NULL,
    source_id text NOT NULL,
    job_id text NOT NULL UNIQUE CHECK (job_id ~ '^job_[0-9a-f]{32}$'),
    nonce_hash bytea NOT NULL CHECK (octet_length(nonce_hash) = 32),
    version bigint NOT NULL CHECK (version > 0),
    builder_id text NOT NULL,
    image_digest text NOT NULL CHECK (image_digest ~ '^[0-9a-f]{64}$'),
    cpu_milli integer NOT NULL CHECK (cpu_milli BETWEEN 100 AND 100000),
    memory_mib integer NOT NULL CHECK (memory_mib BETWEEN 128 AND 1048576),
    storage_mib integer NOT NULL CHECK (storage_mib BETWEEN 128 AND 1048576),
    status text NOT NULL CHECK (status IN ('issued','completed')),
    issued_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL CHECK (expires_at > issued_at),
    PRIMARY KEY (team_id, project_id, delivery_id),
    FOREIGN KEY (delivery_id, team_id, project_id) REFERENCES github_pushes(delivery_id, team_id, project_id),
    FOREIGN KEY (team_id, project_id, source_id) REFERENCES github_verified_sources(team_id, project_id, id)
);

ALTER TABLE github_build_attestations ADD CONSTRAINT github_build_attestations_issued_job
    FOREIGN KEY (team_id, project_id, delivery_id) REFERENCES github_build_jobs(team_id, project_id, delivery_id) NOT VALID;
