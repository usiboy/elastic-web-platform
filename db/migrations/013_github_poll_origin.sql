ALTER TABLE github_deliveries ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'webhook';
ALTER TABLE github_deliveries ADD CONSTRAINT github_deliveries_origin_check CHECK (origin IN ('webhook','app_poll'));
