# P5 局部增量：静态候选与 Host Alias 重启可恢复

**状态：仅本机操作员可信静态候选。** 新项目没有公开租户发布 API、真正隔离构建产物或公网站点；`Project.status=not_deployed` 不变。旧平台 ECS/数据没有修改。

## 本段合同

- `db/migrations/005_static_routes.sql` 分开持久保存 `site_deployments`、`site_hosts` 与追加的 `site_route_events`；部署行明确为 `candidate`，Host 表的代际只表示**期望路由**，不是网关读回的 `active`。迁移器按 SQL 文件 SHA-256 记账、串行提交，改写既有迁移立即拒绝启动。
- `internal/bundle/DiskStore` 按 `(team,project,bundleDigest)` 落实际静态文件字节，重建时与清单重算并拒绝篡改；这是单机开发存储，不具备硬磁盘限额和跨节点可用性。
- `internal/routing/DurableRegistry` 只暴露内部 `RegisterOperatorFixture`，对候选执行内容读回，固定 Host 不能重绑；可变 Alias 用数据库事务 + `expectedGeneration` CAS 切到另一候选，回滚追加新代际事件。`LoadSnapshot` 若路由依赖的对象缺失/篡改则拒绝载入，而不是显示绿灯。临时 `Registry`/Gateway 由快照恢复；后续仍需签名配置与多节点 ACK 才能对公网报告 `active`。

## 真实验证

在与旧平台完全隔离的临时 PostgreSQL 16 `route_test_*` Schema 中，两个模拟 Team 的同名 Project、两个不同静态正文经 `one → two → one` 三次持久 Host 代际切换；每次重新创建 Registry 并加载真实对象，读回 HTTP 正文分别为 `one/two/one`，固定部署 Host 一直是 `one`。过期 generation、跨 Team Host 接管及重绑固定 Host 均被拒绝；向已路由对象写入破坏字节后，恢复流程明确失败。8 路并发以相同 `expectedGeneration` 请求晋升，**恰好一次成功**且终态 generation 为 2。独立的 [P4 真 TLS Host 正反例](2026-10-05-p2-p4-static-contracts.md)验证 HTTPS/SNI 与私有 Preview 访问回调；本段恢复测试的路由快照不是常驻公网网关。

最终 `TEST_DATABASE_URL=<隔离测试库> go test -race ./... -count=1` **通过**：`cmd/api`、`internal/bundle`、`internal/framework`、`internal/httpapi`、`internal/routing`、`internal/source/github`、`internal/source/uploads` 均为 `ok`，`db/migrations` 无单独测试文件；`go vet ./...` **退出 0**。OpenAPI 0.3.0 YAML 解析成功，共 **14** 个 API 路径；**11** 份 Markdown 文档相对链接检查 **0** 处断链。临时 `web_test_*` 和 `route_test_*` Schema 清理后总数 **0**，测试数据库/容器属于新项目独立试验。

进入正式阶段仍需：隔离构建/签名、真实 GitHub/Team 授权、对象存储、浏览器预览会话、真实域名/证书、部署探测和 Gateway 路由版本 ACK/读回、进程重建及跨节点灾备。仅凭本轮本机 HTTP 正文和持久 Host 期望态，不能向租户宣称网站已上线。
