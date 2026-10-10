-- 只表示配额预算与项目预留，不表示已调度运行时或已实施 cgroup/卷硬限。
CREATE TABLE team_resource_budgets (
    team_id text PRIMARY KEY REFERENCES teams(id),
    cpu_milli integer NOT NULL CHECK (cpu_milli BETWEEN 100 AND 100000),
    memory_mib integer NOT NULL CHECK (memory_mib BETWEEN 128 AND 1048576),
    storage_mib integer NOT NULL CHECK (storage_mib BETWEEN 128 AND 1048576),
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_resource_reservations (
    team_id text NOT NULL,
    project_id text NOT NULL,
    cpu_milli integer NOT NULL CHECK (cpu_milli BETWEEN 0 AND 100000),
    memory_mib integer NOT NULL CHECK (memory_mib BETWEEN 0 AND 1048576),
    storage_mib integer NOT NULL CHECK (storage_mib BETWEEN 0 AND 1048576),
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, project_id),
    FOREIGN KEY (team_id, project_id) REFERENCES projects(team_id, id)
);
