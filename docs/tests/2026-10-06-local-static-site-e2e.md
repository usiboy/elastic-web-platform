# 本机静态网站纵向闭环：Agent、真 TLS Preview、生产切流与 Git Worker

**验证日期：2026-10-06。** 这是一条新平台独立 PostgreSQL + 单机存储 + 本机受信测试证书的**受控开发闭环**，不使用旧项目源码/数据库、不操作旧 ECS，不代表任意租户 SaaS 上线或达到 85% 产品/生产准入。

## 交付代码与实际用户行为

1. `cmd/api` + `internal/httpapi`：Owner/Developer/单项目 Agent 以 `sources:upload`/`deployments:create` 权限提交最多 1 MiB 静态 HTML `tar.gz`；服务端取实际字节重新计算 SHA-256、拒绝隐藏文件/构建配置/Source Map，普通 HTML 经 `builtin/static-copy:v1` 生成内容寻址候选，返回 `202 candidate/not_deployed` 和未就绪的 `desiredHost`。Vite/Next.js 不执行于控制进程；Vite 返回 `isolated_build_required`。
2. `cmd/site-gateway` + `internal/routing`：独立 HTTPS 进程从 PostgreSQL 读取持久 Host/代际，从本机内容寻址目录读回实际静态字节；路由刷新失败保留上一健康公共快照并降低 readiness。Owner 获得 60 秒**一次性** Preview 交换码后，站点 Host 用 `Secure; HttpOnly; SameSite=Lax` 的 Host-only Cookie 逐 HTML/JS 请求核对 Team/Project/Deployment 和当前成员令牌；撤权即 401，私有文件 `no-store`。
3. 在隔离真实 TLS Host 的端到端测试中，Agent 先提交 v1 并通过 Preview Cookie 访问首页/JS，网关实际发送首页之后才把部署观察态从 `not_deployed` 更新为 `preview_ready`。Owner 晋升 Production 返回 `202 requested/observedGeneration=0`；**刷新网关前仍没有新域路由**，刷新后真实请求收到 v1 才变 `active/observedGeneration=1`。Agent 提交 v2 并预览，生产旧正文在未刷新期间继续返回；刷新后 v2，随后 Owner 发回滚并重载后恢复 v1，观察代际递增到 3。失效 Owner 令牌使原 Preview Cookie 立即失效。
4. `cmd/site-cli`：受限 Agent 可执行 `deploy/status/preview`，Owner 可 `promote/rollback`；Preview 命令自动在真实 HTTPS Host 交换一次性码并读首页，只输出 Host/状态/正文 SHA-256，不打印交换码、Cookie 或 Bearer。非回环 HTTP 控制 API 被拒。
5. `cmd/source-worker` + `internal/pipeline`：对已签且经运维绑定的 Git push，以单仓库只读 GitHub App 身份重新读取固定 Commit/Tree/Blob、核实际字节，普通 HTML 自动生成**私有**静态候选；Vite 进入 `awaiting_isolated_build` 且产物数为 0。失败事件以有限退避重试，5 次后 `failed`；分支头不再指向该 SHA 时 `superseded`，不会晋升旧代码。仅在 main 分支候选真正被网关观察到、且当前 GitHub 分支头仍匹配固定 SHA 后，Worker 才写入生产**期望**别名，实际状态仍需 Gateway HTTPS 读回。

## 可重放验证与剩余门槛

本轮先写失败测试，再实现最小代码。`TEST_DATABASE_URL=<新项目独立 PostgreSQL 16> go test -race ./... -count=1` **通过**：`cmd/api`、`cmd/site-cli`、`cmd/site-gateway`、`cmd/source-worker`、`internal/bundle`、`internal/framework`、`internal/httpapi`、`internal/pipeline`、`internal/routing`、`internal/source/github`、`internal/source/uploads` 均返回 `ok`；`db/migrations` 无独立测试文件，迁移行为由隔离数据库 API/路由集成测试覆盖。`go vet ./...` **退出 0**；`gofmt -l .` 和 `go mod tidy -diff` **无差异**。OpenAPI 0.5.0 YAML 可解析，**21** 个路径、**116** 个 `$ref` 全部解析；**12** 份 Markdown 相对链接检查 **0** 处断链。数据库测试分别建立 `web_test_*`、`route_test_*`、`pipeline_test_*` 临时 Schema，测试后残留总数 **0**，临时容器与旧平台完全隔离。

**重要边界：**GitHub 仍通过本机模拟 API 验真实 HTTP/令牌范围，没有真实 App 安装、OAuth/Team 归属联调或 GitHub PR Checks；本机真实 TLS 使用隔离证书/本地网络，不等于公用 DNS/正式 CA、WAF、DDoS 清洗或 CDN。静态目录没有累计租户硬磁盘额/多节点对象存储，Gateway ACK 仅单机按实际响应落库；Vite/SSR 未有独立隔离 Builder、恶意代码准入证据。控制台/OIDC/正式计量费率也未实现，故 R1 和“85% 上线”均不能宣称达到。
