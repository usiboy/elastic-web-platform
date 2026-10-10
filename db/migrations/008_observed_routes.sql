ALTER TABLE site_hosts ADD COLUMN IF NOT EXISTS observed_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE site_hosts ADD COLUMN IF NOT EXISTS observed_deployment_id text;
