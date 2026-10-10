-- 实际沙箱构建失败立即记账，不能等租约超时后才发现；失败作业不允许旧签名回放。
ALTER TABLE github_build_jobs DROP CONSTRAINT github_build_jobs_status_check;
ALTER TABLE github_build_jobs ADD CONSTRAINT github_build_jobs_status_check
    CHECK (status IN ('issued','completed','expired','failed'));
