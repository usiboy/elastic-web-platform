# 类 Vercel 网站平台全新实施计划

**Goal：**把“导入 Git 仓库或 Agent 提交 → 自动构建 → 可访问 Preview → 生产域名 → 回滚”作为第一个真实产品闭环。

**Architecture：**新项目从空白服务/数据库起步；Deployment 为中心，控制面与隔离构建/网站入口分离，已就绪制品才可赋予域名。旧版工程和 ECS 当前部署可清退，但任何旧代码/API/DB 不迁入新平台。

**Tech Stack：**Go + PostgreSQL 控制/队列、TypeScript/Vue3 开发者控制台、独立隔离构建资源、对象存储、单区域 HTTPS Gateway；R2 再引入 OCI/Node Runtime。详见[架构](../architecture/2026-10-05-clean-slate-platform-architecture.md)及[产品规格](../product/2026-10-05-vercel-style-product-spec.md)。

## 1. 依赖与实施顺序

```text
P0 产品合同/域名预算定稿 ─┬─ P1 清退旧 ECS/建全新资源 ─┬─ P2 Team/Project/Git/Agent
                         │                          ├─ P3 隔离构建与框架适配
                         │                          └─ P4 单区域 HTTPS/域名
                         └───────────────────────────── P5 真实部署/Preview/Production
                                                       → P6 控制台/CLI/用量/两租户验收 = R1
                                                       → P7 Node/Next.js SSR = R2
                                                       → P8 多区域/Edge/正式账单 = R3/R4
```

优先交付开发者看到的网站部署闭环，**不先做管理员能力中心、通用资源目录或可信评分函数**。每段先写失败测试和实际反例，再完成代码/部署；控制 API、构建沙箱和公网路由应在不同责任边界中逐段验收。开发可以并行，真正启用 Preview/Production 必须等待 P3/P4/P5 均通过。

| 阶段 | 可验收产物（拟建路径） | 退出门槛 |
|---|---|---|
| P0 合同与输入 | `docs/product/`、`docs/architecture/`、`openapi/platform.yaml` 初稿、框架支持矩阵 | 定稿默认域、GitHub App、生产自动切流/预览保护、测试账号与隔离资源上限；成本/数据驻留由所有者确认 |
| P1 清场与独立基础设施 | `docs/operations/2026-10-05-ecs-rebuild-runbook.md`、`deploy/control/`、`deploy/build/`；**无数据迁移** | 旧运行资产和原站点依赖已盘点/备份/可恢复；测试 DNS/证书、DB/对象存储、独立 Builder 就绪；恢复/回退演练通过 |
| P2 团队与来源 | `cmd/api/`、`internal/teams/`、`internal/projects/`、`internal/source/github/`、`internal/source/uploads/`、`db/migrations/001_*.sql`、`openapi/platform.yaml` | 浏览器能列出**已获授权的真实仓库**；Agent 可以受限上传；Webhook 验签+二次拉取，跨 Team/越 Repo/重放拒绝；重启保留 Project |
| P3 框架与隔离构建 | `internal/framework/`、`internal/build/`、`cmd/builder/`、`internal/bundle/`、`deploy/build/`、`docs/tests/r1-builder-admission.md` | Vue/React Vite/静态 HTML 真仓库均从源码成功构建并得到可读取 Bundle；恶意依赖/PID/磁盘/超时/越权测试在目标 Worker 阻断；B 类高开销 React 构建有真实容量基准；构建失败不产出 READY |
| P4 公网站点入口 | `cmd/site-gateway/`、`internal/routing/`、`internal/domains/`、`internal/protection/`、`deploy/gateway/` | 单区域真实 DNS+TLS；两个不同 Host 不串站，来源域/预览域分离；私有 Preview 每个资源都鉴权，域名所有权和证书状态分别可核 |
| P5 自动部署与回滚 | `internal/deployments/`、`internal/aliases/`、`internal/gitchecks/`、`internal/events/`、`db/migrations/002_*.sql` | 首部署生成默认生产 URL；后续分支/PR 有固定和分支 URL，主分支发布只在路由/内容读回后切域；失败保留旧生产，回滚无须重新构建；PR 状态/链接正确 |
| P6 交付者体验与用量 | `console-web/`、`cmd/site-cli/`、`internal/metering/`、`internal/quotas/`、`db/migrations/003_*.sql`、`docs/tests/r1-e2e.md` | 真实 A/B 两租户、Git 与 Agent 两种来源从零完成；无集群术语的页面，日志/用量/缺测可见；旧应用不可枚举、恶意流量不入租户试算；R1 放行决策记录 |
| P7 动态站点 | `internal/runtime/`、`internal/framework/next/`、`deploy/runtime/`、`docs/tests/r2-ssr-admission.md` | 声明可支持的 Node/Next.js 版本；真实 SSR/API/流式响应、冷启动/热复用、超时进程终止、出站与数据库连接上限；生产回滚实际生效 |
| P8 多区域/边缘/计费 | `internal/edge/`、`internal/billing/`、`deploy/regions/`、`docs/tests/r4-capacity-billing.md` | 配置签名版本/ACK、故障域恢复、V8 支持边界与 ISR 失效、压测/事件对账、法务/财务定价审批后才开正式账单 |

**2026-10-05 首段进度：**批次 A 的 Team/Project/短期 Agent 令牌控制 API、独立 PostgreSQL 表与 OpenAPI 已在 `cmd/api/`、`internal/httpapi/`、`db/migrations/`、`openapi/` 落地，并在隔离数据库通过测试；详见[首段验证记录](../tests/2026-10-05-p2-project-identity-api.md)。当时 GitHub App 接入和 Agent 上传尚未实现；P1 ECS 清退未开始。`not_deployed` 是此阶段唯一正确的项目运行态。

**同日来源收件增量：**Agent tar.gz 归档路径/类型/尺寸限制、单机内容寻址字节保存及 Postgres 来源元数据已通过正反例；GitHub HMAC webhook 去重、运维指派的仓库绑定和单仓库安装令牌元数据复核在**本机模拟 GitHub API**通过，见[来源收件验证](../tests/2026-10-05-p2-source-ingestion.md)。前段“尚未实现 Agent 上传”仅描述先前时点；当前仍没有真实 GitHub App 身份/用户自助仓库选择、固定提交源码包取回和可信构建。P2 尚未验收，Project 保持 `not_deployed`。

**同日 P2–P4 合同增量：**已在模拟 GitHub API/隔离 PostgreSQL 重新读取固定 Commit/Tree/Blob，并按真实字节生成确定性来源归档；已实现 Vue/React Vite 的无执行框架检测与静态 HTML 内置字节复制，以及单机注册表的固定 Host、别名 CAS/回滚和双 Host 真 TLS 私有预览拒绝测试，见[分段记录](../tests/2026-10-05-p2-p4-static-contracts.md)。上段的“尚无固定提交源码包取回”仅是前一时点。P2 缺 GitHub Team 用户授权和自动任务、P3 缺独立隔离构建器、P4 缺持久路由/真实域名及正式 Preview 身份，均不能记为生产阶段验收。

**同日 P5 本机持久增量：**内部可信静态候选现在分别在 PostgreSQL 记录 Deployment/Host Alias 代际与追加事件、在本机受限目录按摘要存真实静态字节；读取新 Registry 快照核对字节/归属，重建后 `one → two → one` 以及固定 Host 不漂移、8 路并发晋升仅一个成功，见[P5 记录](../tests/2026-10-05-p5-durable-routes.md)。上述 P4 的“缺持久路由”仅是上一时点；现在仍缺**多节点** Gateway ACK、真实域名/证书、公开自助入口和隔离构建，不能称正式生产部署。

**2026-10-06 本机纵向切片：**[本机 E2E 记录](../tests/2026-10-06-local-static-site-e2e.md)覆盖 Agent 上传 HTML→受保护真 TLS Preview→一次性会话/撤权→生产别名 `one→two→one` 并在每步区分期望/实际代际；新增独立 `cmd/site-gateway`、`cmd/site-cli`、`cmd/source-worker`。Git 签名事件经单仓库读取实际 Commit/Tree/Blob，由持久 Worker 自动生成普通静态站点候选；生产分支只有在 Preview 已被实际读取且 Git 当前分支头仍是该 SHA 时自动发晋升意图。隔离构建、真实 GitHub App 用户安装/归属、正式 DNS/TLS、OIDC、费用归因及多节点准入未完成，**不能按页面数量或本机 fixture 把 R1 标为 85%/已上线**。

**阶段验收口径：**P2 的静态来源/待处理事件子链、P3 的无脚本静态复制、P4 的本机真 TLS Preview、P5 的本机持久别名及观察态、P6 的 Agent CLI 均有可重放代码证据；但 P1 ECS 清退/独立 Worker 资源、P2 真人 GitHub 授权、P3 不可信 Vite 构建隔离、P4 正式 DNS/证书、P5 多节点 Gateway ACK、P6 浏览器开发者控制台和实测用量仍是独立阻断。下一实施顺序：**独立 Builder 与对象存储/累计硬配额 → 真 GitHub 用户授权与两仓库 → 公网域名/受信证书和多节点路由读回 → 项目优先控制台/用量对账**。未验收的项保持 blocked，不能把本机测试比例换算为生产总体进度。

**同日 GitHub Owner 自助归属合同：**[OAuth/授权验证记录](../tests/2026-10-06-github-oauth-ownership.md)补齐了前述 P2 的**代码合同**：Owner 用 GitHub App 用户 OAuth（随机 state + PKCE）证明本人对安装内的 Repo 有 `pull` 权，再用 App 单仓库令牌独立复核；跨 Team、Viewer、过期/重放/撤权均拒绝。持久绑定只记录 GitHub 数字用户 ID，不保存用户访问/刷新 Token；App 用户授权撤销及安装删除/仓库移除签名 webhook 停止新任务，重新授权才恢复。仍未在真实 GitHub App/两套账号、公网回调与组织权限变化中验收；“用户仓库选择器”及 Team 的浏览器会话仍是产品工作，而非一个表单已等于 Vercel 导入体验。

**同日 P6 开发者工作台本地切片：**[Vue3 浏览器与 API 验证](../tests/2026-10-06-project-first-console.md)已实现 Projects 首页、项目 Overview/Deployments/Settings、真实 API 分页、Owner/Viewer/Agent 状态与静态源码提交入口；状态来自 `/v1/me`、项目、Git 绑定和别名读回，未部署/不可核实显示真实空态，生产未读回不展示上线链接。浏览器令牌仅留在当前页面内存；没有 OIDC/仓库选择器/真实两租户浏览器业务验收。此前“P6 浏览器控制台缺失”是前一时点记录，不改变 R1 SaaS 准入阻断。

**同日真实 GitHub 与 ECS 私有 Preview：**[两仓库来源/受控构建](../tests/2026-10-06-real-github-ab-build.md)验证 App 的单仓库只读令牌、固定真实 Git 字节及不依赖 webhook 的主分支轮询，Vite 来源停在 `awaiting_isolated_build`；B 默认构建仍停在 Rollup transform，临时关闭 tree-shaking 才得到诊断产物。[旧控制 ECS 上新平台独立入口](../tests/2026-10-06-ecs-private-preview-ab.md)验证两个**操作员可信 fixture** 的测试 CA HTTPS 私有 Preview、A/B 身份隔离、实际 HTML/JS 与 `preview_ready` 读回，旧 IP 匿名 401 未变。正式 DNS/公网 CA、用户 OAuth、自助租户构建、生产发布/回滚、强隔离与硬配额仍未验收；P1/P3/P4/P5/P6 及 R1 上线均不得由此记为完成。

**同日单租户模板与 HTTP 静态别名：**用户允许 HTTP 公共页面并授权将公开 testA 替换为剔除真实数据的[纤莉秀快麦模板](../tests/2026-10-06-xianlixiu-http-template-fit.md)。testA GitHub Actions CI 成功；新平台操作员可信候选经私有 Preview 读回后，由 Owner 显式晋升 HTTP 生产 Host 为 `active`，旧站与 B 租户无回归。此处 CI、手动 CD 和静态内容分配已经分段可验；完整 Node/SQLite/CLI 动态应用、自动 Vite 构建、通用强隔离和 CPU/内存/磁盘配额仍未达到平台准入。此前段落的“生产发布未验收”只描述当时的 A/B 私有 Preview 阶段。

**同日资源控制与浏览器验收增量：**独立库新增[Team 预算与项目预留](../tests/2026-10-06-resource-reservation-control.md)，Owner 控制面可以按版本申请/释放、并发超售拒绝；控制台与纤莉秀已部署静态字节经过[A/B 浏览器桌面/移动及 API 409 验收](../tests/2026-10-06-browser-ui-acceptance.md)，仍无运行时硬配额、自动 Builder、自助导入或真实业务托管。[R1/R2 差距收敛设计](2026-10-06-r1-r2-gap-closure.md)列明独立节点、域名证书、用量/日志和纤莉秀动态准入的退出证据，不把逻辑预留提升为 P3/P6 完成。

## 2. R1 的可执行任务批次

下列为各批次施工合同；批次 A 中已落地的文件和验证以本节上方的真实记录为准，其余路径为未来计划，不表示已存在或测试已通过。新仓库模块路径在建立远端时可再调整；只有用户明确要求 Git 提交后才分段提交。

### 批次 A：项目身份和产品 API（P2）

1. 在 `internal/teams/teams_test.go`、`internal/projects/projects_test.go`、`internal/httpapi/projects_test.go` 写匿名、Owner/Developer/Viewer、Agent 过期/越项目、A/B 同名项目与跨租户 404 失败测试；运行 `go test ./internal/teams ./internal/projects ./internal/httpapi` 先观察失败。
2. 首段实现实际路径为 `db/migrations/001_identity_projects.sql`、`internal/httpapi/projects.go`、`cmd/api/main.go`；Project 归 Team，Agent scope 由服务端认证，写入带幂等请求/审计，仓库连接尚未就绪。后续复杂度增加时再按职责拆成 `internal/teams/`、`internal/projects/`，避免首段引入空模块。
3. 运行 `go test -race ./...`、`go vet ./...` 和独立测试数据库重启/回放；期望项目归属和成员撤销持续有效。浏览器页面不需要运维管理员先配置“目标服务”。

### 批次 B：Git/Agent 与构建沙箱（P2/P3）

1. `internal/source/github/webhook_test.go`、`internal/source/github/installation_test.go`：原始字节 HMAC、delivery 幂等、安装卸载、仓库范围、SHA 强推后的不可变部署；`internal/source/uploads/upload_test.go`：总量/路径/重复字节/令牌作用域；跑定向测试先失败。
2. 创建 `internal/source/github/`、`internal/source/uploads/`、`internal/framework/detect.go`、`internal/framework/vite.go`、`internal/bundle/manifest.go`、`internal/build/scheduler.go`、`cmd/builder/main.go`，声明并生成 Bundle v1；固定构建器版本/lockfile，远程缓存按输入摘要命中且无秘密，失败保留有界日志。通过 Git ref 导入或 Agent 文件上传都走同一 Build/Deployment 管线。
3. `deploy/build/` 部署独立隔离 Builder；编写 `docs/tests/r1-builder-admission.md` 记录目标机器/Runtime/PID/磁盘/网络/超时反例和邻居状态。验收 Vue/React 真仓库，尤其要以相同资源规格重复验证 React 构建，不以旧项目失败的 B 构建结果代表新环境。安全拒绝若失败，不开放 R1 自助上传开关。

### 批次 C：真实站点和别名（P4/P5）

1. `internal/deployments/deployments_test.go`、`internal/aliases/aliases_test.go`、`internal/routing/routing_test.go`：同 commit 不同环境应产生不同版本、失败不切别名、并发 CAS、旧版回滚、错误 Host、部分路由 ACK、证书未就绪、静态资源缓存泄露的失败用例。
2. 创建 `db/migrations/002_deployments_routes.sql`、`internal/deployments/`、`internal/aliases/`、`internal/routing/`、`cmd/site-gateway/`。完成对象读取/hash、Gateway 版本和 readback 后才能从 `DEPLOYING` 变 `READY`，然后首个生产或分支 Alias 原子切换；固定 URL 的路由永不指向后来的产物。
3. 从真实 TLS Host 用浏览器与 HTTP 客户端访问两租户：第 1/2 次提交、失败第 3 次、主分支切生产、Rollback；PR check 的链接对应实际版本。运行定向集成测试、`go test -race ./...`、`go vet ./...`；完整恢复/回退和证书失配记录到 `docs/tests/r1-e2e.md`。

### 批次 D：开发者控制台和试算（P6）

1. `console-web/` 实现 Products 首屏（Projects 列表、Import、Project Overview、Deployments、Build Log、Domains、Usage）；E2E 用真实 API 完成 Team A/B 登录、Git 授权、失败与回滚。页面由状态驱动，草稿不能渲染可点击的 Preview URL。
2. `cmd/site-cli/` 对 OpenAPI 编排 `deploy --preview`、`inspect`、`logs`、`promote`；Agent 收到 `202` 后轮询至 READY/FAILED，不把“命令返回 0”当已上线。CLI 只获取项目受限凭据，不读取全局云端凭据。
3. `internal/metering/`、`internal/quotas/` 存去重请求/构建事件和限额，WAF 清洗前后分开；测试公开匿名合法访问、恶意丢弃、消息重复、丢失采样、预算耗尽。试算有来源/采样时间；无经过审查的 RateCard 时不出金额。

## 3. 上线判据与边界

**R1 只有在以下全部完成时才能称为产品上线：**真实两个互相独立的 Team，真实 GitHub/Agent 源码，Vue/React 在隔离 Builder 中可重复构建，两个公开/私有 HTTPS 站点正常访问，自动 Production/Branch Preview、回滚和跨租户拒绝均实际通过；DB/对象/路由故障和恢复实测；操作者可通过 UI/CLI 找回日志并知道错误原因；既有资源回退方案可执行。只有单 ECS 而无经验证独立 Builder 时，R1 `blocked`，允许内部原型但不开放陌生租户构建。

**下一步优先确认：**现有 ECS 可否停机清场（包括原 IP HTTPS/18092/18093 是否仍有人使用）、默认域名控制权、GitHub App 所有者、允许采购/使用的隔离 Builder 与对象存储预算。无需把旧系统已做成的固定可信服务当新产品基础。
