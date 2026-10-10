# 新平台首段验证：Team / Project / Agent 控制 API

**日期：2026-10-05。结论：P2 的身份与项目草稿子任务完成本机隔离验证；不等于 P2 Git/Agent 上传完成，也不等于网站可部署。** 当前所有 Project 的 `status=not_deployed`。

## 实际实现

- `go.mod`、`cmd/api/`：独立 Go HTTP 控制进程，默认只监听 `127.0.0.1:8097`；公网监听要求 TLS 证书与私钥同时提供；数据库和引导凭证缺失拒绝启动。
- `db/migrations/001_identity_projects.sql`：独立 PostgreSQL 的 Team、Project、哈希令牌、幂等请求与审计表；`db/migrations/migrate.go` 内嵌首次迁移。此库与旧 `elastic-compute-platform/` 无关系。
- `internal/httpapi/projects.go`：运维引导 Team；Owner/Developer 创建项目，Viewer 只读，Agent 限定单 Project `projects:read`，访问被撤销/过期立即拒绝。`Idempotency-Key` 相同重试复用项目，异请求冲突；跨 Team 404。令牌使用随机值且数据库仅存 SHA-256 摘要，首次响应后不再返回明文。
- `openapi/platform.yaml`、[运行说明](../operations/local-development.md)：只公开目前确实实现的七条路径与数据状态。

## 测试方式和结果

测试库为**本机临时** `docker.1ms.run/library/postgres:16-alpine`（镜像摘要 `sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea`），Docker 随机映射回环端口；未连接旧平台数据库。每个测试建立 `web_test_*` 独立 schema，退出删除；验后剩余测试 schema 数 **0**。

1. 测试先因 `NewServer`/迁移实现缺失失败；实现后，新增并发同键重试负例曾得到 409，定位到两个并发引导事务同时查询空幂等行，增加同键事务锁后通过。非 Agent 令牌创建曾得到 SQLSTATE `23502`（`actions` 列），定位为 pgx 将 nil 切片发为 `NULL`，改为显式空数组后通过。超过 100 条项目列表曾没有续页，补充稳定游标后通过。
2. `TEST_DATABASE_URL=<专用测试库连接> go test -race ./... -count=1 -v`：`cmd/api` 配置测试、`internal/httpapi` 五项顶层集成测试全部 **PASS**；涵盖 A/B 同名项目/跨租户 404、项目重启持久、Owner/Viewer/Agent 权限、撤销和过期、同键并发、101 条列表续页。
3. `go vet ./...`：**通过**。Ruby YAML 解析 `openapi/platform.yaml`：**通过**，得到 7 个路径；Markdown 相对链接检查：**0 处断链**。

## 未通过的产品边界

没有真实站点构建、Git 安装、Agent 上传、任何 Preview/Production URL 或云端部署；无 ECS 操作、域名/证书切换。当前引导身份仍是运维短期 Bearer 令牌，尚无用户登录/团队联合身份和正式密钥托管；数据库迁移尚需版本化与升级回滚合同。R1 多租户自助执行前仍需独立隔离构建 Worker 与真实恶意代码/容量测试。下一段应实现 GitHub App 仓库归属和 Agent 源码上传的**仅接收/校验**合同，再启动隔离构建链路。
