-- 过期作业持久终态：构建卡住时前端不再永远显示“待隔离构建”。
ALTER TABLE github_build_jobs DROP CONSTRAINT github_build_jobs_status_check;
ALTER TABLE github_build_jobs ADD CONSTRAINT github_build_jobs_status_check
    CHECK (status IN ('issued','completed','expired'));
