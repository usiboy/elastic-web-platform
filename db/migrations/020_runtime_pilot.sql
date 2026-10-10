-- R2 内部合成运行时试点；端点只能由受控操作员登记，绝不暴露给租户 API。
CREATE TABLE site_runtime_pilots (
    deployment_id text PRIMARY KEY,
    team_id text NOT NULL,
    project_id text NOT NULL,
    upstream_port integer CHECK (upstream_port BETWEEN 1024 AND 65535),
    upstream_socket text CHECK (upstream_socket ~ '^/run/ewp-r2-[a-z0-9-]+/[a-z0-9.-]+[.]sock$'),
    CHECK ((upstream_port IS NULL) <> (upstream_socket IS NULL)),
    max_concurrency integer NOT NULL CHECK (max_concurrency BETWEEN 1 AND 32),
    timeout_ms integer NOT NULL CHECK (timeout_ms BETWEEN 100 AND 60000),
    state text NOT NULL CHECK (state IN ('ready','failed')),
    FOREIGN KEY (team_id,project_id,deployment_id) REFERENCES site_deployments(team_id,project_id,id)
);
