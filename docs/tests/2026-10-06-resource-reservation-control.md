# 纤莉秀测试租户的持久资源预算与项目预留控制面

**日期：2026-10-06。性质：预算登记与并发预留仲裁，不是 ECS 资源配给或运行时硬限。** 新迁移 `015_project_resource_reservations.sql` 为 Team 设置独立 CPU 毫核、内存 MiB、存储 MiB 预算，为 Project 保存版本化预留。两个层级统一锁定同一 `teams` 行、在事务内合计已预留额度；预算缩小、项目并发超售和过期 `expectedVersion` 均返回 409。Owner 只能申请本 Team Project 资源，Team 预算仅可用服务端 Bootstrap 身份配置，Viewer/Agent 不可写，项目级 Agent 也不能查看 Team 总预算。预留总和使用单条 SQL 快照供工作台展示。

## 实现与本地验证

- `PUT /v1/admin/teams/{teamId}/resource-budget`：`expectedVersion=0` 首次创建，此后逐次 CAS 更新；不能小于已预留合计。
- `GET /v1/teams/{teamId}/resources`：返回 `budget`、`reserved`、`available`、`version`，明确 `budgeted_not_provisioned`。
- `GET/PUT /v1/teams/{teamId}/projects/{projectId}/resources`：未分配返回 `unallocated`，成功预留返回 `reserved_not_provisioned`；三项全部为零显式释放预算。
- `TestProjectResourceReservationsRejectOvercommitAcrossProjectsAndRestart` 在独立 PostgreSQL Schema 中让两个项目并发申请各 600m/640 MiB，Team 只有 1000m/1024 MiB：恰好一个 201、一个 409；重新创建 API handler、重放迁移后汇总不漂移，跨 Team 查询 404，预算不能缩到已预留额度以下。`TestTeamBudgetChangeAndProjectReservationShareCapacityLock` 同时收缩 Team 预算与申请 Project 预留，恰好一个成功，重放读回预算始终不低于已预留量；成功预留后的显式释放允许继续收缩。Owner/Viewer/Agent、CAS、负数等另有正反用例。
- 独立本机 PostgreSQL 下 `go test -race ./... -count=1` 各包通过，`go vet ./...` 无诊断；`console-web/` 的 `npm test` 4 项通过，带 `VITE_SITE_SCHEME=http` 的 `npm run build`（含 `vue-tsc`）通过；OpenAPI v0.9 YAML 语法校验通过。

## ECS 受控应用

旧控制 ECS 的新平台专用测试库在迁移 015 前备份 `pre-migration15.dump`（已用 `pg_restore --list` 核验，SHA-256 `f773f00b45c51e3cf252b050dbea1732969b9b73ad69d06486a887aec921ab2d`）；新平台 API Linux binary SHA-256 `4434231961dd89617f9f6ccb622907453298219cdcc25acd9b6203809972a0b1`，只替换新平台 transient unit 的目标 binary，保存原 binary 以便回退。匿名 `/v1/me` 仍 401，迁移账本确认 015 恰好一次。原 IP 服务、旧数据库及独立网关未替换。

从原纤莉秀项目 `/Users/liumingjian/Development/workbench/纤莉秀/docs/内网部署检查清单.md` 的实际 1 GiB 限制、OOM 时建议 2 GiB 内存出发，只给本轮测试租户登记以下**实验性**值；并未写入旧项目、使用公网业务身份或创建容器/持久卷：

| 测试 Team/Project | Team 预算 | Project 预留 | 剩余额度 |
|---|---|---|---|
| A：纤莉秀模板 `p_34401f5c05af1079010cb95ee3ec1275` | 1500m / 4096 MiB / 4096 MiB | 1000m / 2048 MiB / 2048 MiB | 500m / 2048 MiB / 2048 MiB |
| B：原 `testB` `p_e5123bae12388454bb82fc1590750ab9` | 1000m / 2048 MiB / 1024 MiB | 500m / 512 MiB / 256 MiB | 500m / 1536 MiB / 768 MiB |

实验性写入前已备份独立测试库 `pre-test-budget-seed.dump`（SHA-256 `21a7277ae618c46a746332208ab370e812f508aa4535896ce25bdb21d67da6a2`）；私有 Bootstrap/Owner 文件仅在 ECS 本机由脚本送往回环 HTTPS API，没有写入源码、日志或终端输出。B 用过期容量申请 1200m 得到 409；公网 opt-in 测试在 A/B Owner 下分别读回自己 Team 和 Project 的预留，跨 Team 项目资源查询 404。私有 A/B Preview 与 testA 公共 HTTP 首页、静态 JS、生产观察态及旧 IP 401 回归通过。工作台 Settings 现在显示数据库预算与预留状态，Owner 可用版本号申请或释放；工作台仅展示 API 读回，不把“预留”展示为真实用量。另见[实际浏览器操作](2026-10-06-browser-ui-acceptance.md)。

## 仍未满足的运行时与治理条件

Team 预算是**控制数据库的逻辑上限**，目前没有全局节点容量池，也没有把预留转换为 Docker/Kubernetes cgroup、PID、磁盘配额或实际对象存储容量；原纤莉秀的 Node/Express/SQLite/`kuaimai-cli` 没有由新平台调度。现有 ECS `4` 个 CPU，内存约 `15 GiB`、`/data` 可用约 `35 GiB`，但旧站同机运行，这些测试预留**不得**解释成独占物理分配。达到完整资源管理前需为独立 Builder/运行节点建立全局容量注册和实际 ACK，调度时按已预留上限应用硬限，记录用量/超额/回收，验证真实纤莉秀商品全量同步与货盘生成的峰值内存和持久卷恢复。仍需独立构建资源确认、真实业务身份安全访问方式和自动 CD；本阶段不能标记完整 SaaS/动态应用上线。

**2026-10-07 后续变更：**用户授权原 ECS 的同机 Builder 试点后，B 构建需要 2 CPU/3 GiB。独立新平台库写入前备份 `pre-testb-budget-builder.dump`（SHA-256 `170b1b1d1114ad7c1aece617d0814057f5cf89236e6f6ba6767e79051f2a8882`）；B Team 预算按 CAS v1→v2 改为 2500m/4096/4096 MiB，B Project 预留改为 2000m/3072/2048 MiB，A 的预留不变。上表只是**前一时点**实验值，当前状态见[同机 Builder 实测](2026-10-07-original-ecs-builder-ab.md)。试点容器实际按 B 2 CPU/3 GiB、A 1 CPU/2 GiB 限制并串行构建，但逻辑预留仍不是全局节点容量 ACK，输出卷无总磁盘硬额。
