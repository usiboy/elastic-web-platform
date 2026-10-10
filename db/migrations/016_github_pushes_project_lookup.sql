-- 项目级 Git 事件状态列表只在该 Team/Project 范围内扫描，不作跨租户聚合。
CREATE INDEX github_pushes_team_project_lookup ON github_pushes(team_id, project_id, delivery_id);
