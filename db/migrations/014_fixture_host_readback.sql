-- 补齐早期操作员可信静态候选的固定 Host 元数据；只读取本项目 site_hosts。
UPDATE site_deployments AS deployment
SET desired_host = host.hostname
FROM site_hosts AS host
WHERE deployment.id = host.deployment_id
  AND deployment.team_id = host.team_id
  AND deployment.project_id = host.project_id
  AND host.is_fixed = true
  AND deployment.desired_host IS NULL;
