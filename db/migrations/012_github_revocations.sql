ALTER TABLE github_bindings ADD COLUMN IF NOT EXISTS authorized_github_user_id bigint;
ALTER TABLE github_bindings DROP CONSTRAINT IF EXISTS github_bindings_status_check;
ALTER TABLE github_bindings ADD CONSTRAINT github_bindings_status_check CHECK (status IN ('pending','verified','revoked'));

CREATE INDEX IF NOT EXISTS github_bindings_by_authorized_user
    ON github_bindings(authorized_github_user_id) WHERE status='verified';

CREATE TABLE IF NOT EXISTS github_revocation_deliveries (
    delivery_id text PRIMARY KEY,
    payload_hash bytea NOT NULL,
    event text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
