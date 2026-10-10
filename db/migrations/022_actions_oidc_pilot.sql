-- 仅批准两个公开测试仓库：每次 GitHub Actions push 的 OIDC 身份只能生成一条固定来源。
ALTER TABLE github_deliveries DROP CONSTRAINT github_deliveries_origin_check;
ALTER TABLE github_deliveries ADD CONSTRAINT github_deliveries_origin_check
    CHECK (origin IN ('webhook','app_poll','actions_oidc'));

CREATE TABLE github_actions_receipts (
    repository_id bigint NOT NULL,
    run_id bigint NOT NULL,
    run_attempt integer NOT NULL CHECK (run_attempt > 0),
    jti_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(jti_sha256) = 32),
    delivery_id text NOT NULL UNIQUE REFERENCES github_deliveries(delivery_id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id,run_id,run_attempt)
);
