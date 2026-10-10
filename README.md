# 弹性网站平台：从 Git 到网站的独立交付产品

**状态：同 ECS 合成 Next.js R2 Stage 双版本回退、原纤莉秀 Express/React + 合成 SQLite 私有 Preview，以及修复 PostgreSQL 启动问题后的整机自动恢复均已实测；未通过共享 SaaS/真实快麦业务准入。** 这是面向用户与 AI Agent 的类 Vercel 网站平台新项目。旧项目 `../elastic-compute-platform/` 的代码、数据库、控制台、状态机和服务发布模式不迁入本项目；旧 ECS 服务保持运行，新平台使用独立目录、服务、数据库及 Host。

## 一句话产品

选择仓库或让 AI Agent 提交项目 → 自动识别框架并构建 → 每次提交获得可访问的 Preview → 合并主分支自动上线 Production → 绑定域名、观测、回滚。平台独立运行；绫通仅是首个带来租户与套餐的接入渠道。

## 从哪里读起

1. [产品规格与真实用户旅程](docs/product/2026-10-05-vercel-style-product-spec.md)：明确首发范围、用户界面与验收。
2. [全新技术架构](docs/architecture/2026-10-05-clean-slate-platform-architecture.md)：部署对象、Git/Agent/运行时、控制面和数据面。
3. [实施路线](docs/plans/2026-10-05-rebuild-roadmap.md)：从清场到 Git→Preview→Production，再到动态计算与全球边缘。
4. [ECS 清退与重建预案](docs/operations/2026-10-05-ecs-rebuild-runbook.md)：资产盘点、备份、隔离、回退、重装准入。
5. [官方依据与提示词](docs/knowledge/2026-10-05-vercel-reference-and-prompts.md)：产品行为出处与后续设计审查模板。
6. [本机控制 API 运行指南](docs/operations/local-development.md)与[当前 OpenAPI](openapi/platform.yaml)：Team、Project、Agent 静态 HTML 提交、Git 自动来源核验及生产别名。
7. [P2–P4 验证记录](docs/tests/2026-10-05-p2-p4-static-contracts.md)：框架识别、不可变静态字节和本机 TLS 路由/回滚的真实测试边界。
8. [P5 本机持久别名验证](docs/tests/2026-10-05-p5-durable-routes.md)：PostgreSQL 代际、独立制品字节、重建与并发回滚。
9. [2026-10-06 本机静态站点端到端验证](docs/tests/2026-10-06-local-static-site-e2e.md)：真实 TLS Preview 会话、Agent CLI、观察态与 Git 事件 Worker。
10. [GitHub Owner 授权与撤销记录](docs/tests/2026-10-06-github-oauth-ownership.md)：用户/App 双重仓库证明、PKCE、失权撤销及重放拒绝。
11. [项目优先工作台验证](docs/tests/2026-10-06-project-first-console.md)：真实 API 驱动的 Vue3 项目/部署页、角色和浏览器空态。
12. [纤莉秀模板、HTTP 交付与平台适配](docs/tests/2026-10-06-xianlixiu-http-template-fit.md)：真实 testA 提交、CI、操作员可信候选、HTTP 生产别名及尚未满足的动态运行/资源管理条件。
13. [Team 预算与 Project 预留控制面](docs/tests/2026-10-06-resource-reservation-control.md)：并发超售阻断、A/B 测试租户读回和未配给运行时的准入边界。
14. [A/B 工作台与纤莉秀 Web UI 浏览器验收](docs/tests/2026-10-06-browser-ui-acceptance.md)及[R1/R2 差距收敛设计](docs/plans/2026-10-06-r1-r2-gap-closure.md)：桌面/移动交互、资源超额提示、可访问性和剩余真实准入任务。
15. [隔离 Builder 前置准入](docs/tests/2026-10-06-builder-preflight-gate.md)：真实节点缺席、`runc` 拒绝、Vite 构建命令约束与 Git 来源仍待构建的现场证据。
16. [Git 状态项目列表与浏览器读回](docs/tests/2026-10-06-git-status-browser.md)：按项目分页发现真实 Git 事件、跨租户隔离、工作台待构建说明与迁移 016。
17. [签名 Builder 交接内部合同](docs/tests/2026-10-06-signed-builder-handoff.md)：本机迁移 017、固定 Git 来源/产物签名、失权和重放测试；未接真实 Builder 或 ECS。
18. [一次性 Git 构建作业租约](docs/tests/2026-10-06-build-job-lease.md)：本机迁移 018、Project 预留、作业 nonce/CAS/过期与签名产物绑定；线上仍未启动不可信构建。
19. [真实 testA/testB Git、CI 与独立 VM 构建复验](docs/tests/2026-10-06-real-git-ab-ci-independent-vm.md)：testB 专属修复、新提交 CI 成功、两仓库静态字节一致、B 新私有操作员 fixture 和仍未完成的强隔离准入。
20. [ARM64 gVisor `runsc` 双仓库构建与本机签名交接](docs/tests/2026-10-06-runsc-real-ab-handoff.md)：官方 SHA-512、独立 VM 沙箱、两次真实 A/B 离线构建、CI 字节一致、真实来源进入本机作业/私有候选及仍未上线的信任门槛。
21. [原 ECS 同机独立网络命名空间 Builder 试点](docs/tests/2026-10-07-original-ecs-builder-ab.md)及[网络恢复运维说明](docs/operations/2026-10-07-same-ecs-builder-runbook.md)：保留旧站的 cgroupfs/runsc 第二 daemon、A/B 固定源码构建和私有 Preview、实际事故与恢复证据。
22. [同机 Builder 真 Git→一次性作业→签名私有 Preview](docs/tests/2026-10-07-same-ecs-git-signed-ab.md)：短期只读单仓库 Token 核源、签名作业、真实 A/B HTML/JS Gateway 读回、Token 撤销与仍未放行的自动上线边界。
23. [同机 Builder 固定 A/B 输出硬限实测](docs/tests/2026-10-07-bounded-builder-output.md)：新输出卷每项目 64 MiB、真实 `runsc` 超额 `ENOSPC`、A/B 逐字节构建及旧站回归；仍非全局存储硬额或共享 SaaS 准入。
24. [构建作业超期失败与旧版保留](docs/tests/2026-10-07-r1-build-job-expiry.md)：迁移 019、两个并发扫描者只处理一次超期作业、旧生产别名不变；本机合同未上线。
25. [R2 Node/Next.js 合成动态运行探针](docs/tests/2026-10-07-r2-node-next15-synthetic-probe.md)：Node 22.22.0 与 Next 15.5.27 的真实 `runsc` SSR/API/流式响应、超时杀停及原 ECS 旧站回归；仍未接入平台动态部署。
26. [R2 动态路由与纤莉秀数据迁移前置验证](docs/tests/2026-10-07-r2-route-runtime-and-xianlixiu-readiness.md)：迁移 020 本机动态路由试点、会话隔离与失败回退、原应用 SQLite 单写者和可校验备份；上线仍 blocked。
27. [同 ECS 合成 R2 功能闭环实测](docs/tests/2026-10-07-r2-same-ecs-functional-loop.md)及[运行回退说明](docs/operations/2026-10-07-r2-synthetic-loop-runbook.md)：内部 TLS Preview/Stage→真实 `runsc` Next、失败自动回静态、回收后新版本再发布；不涉及真实业务数据库。
28. [R2 双动态版本故障回退实测](docs/tests/2026-10-08-r2-dynamic-version-rollback.md)：Stage v2→v3 切流、v3 504 后代际回 v2、仅清理 v3 并保留 v2 服务，旧静态/旧站仍健康。
29. [R2 合成实例启动链及停启演练](docs/tests/2026-10-08-r2-runtime-recovery.md)：控制 API/网站网关持久 systemd、仅新平台 PostgreSQL 自动启动、同一 `runsc` 容器 ID 的停止/恢复与 Stage 代际不变；本段当时尚未实测整机重启，后续结果见第 31 条。
30. [原纤莉秀应用合成数据私有 Preview](docs/tests/2026-10-08-xianlixiu-synthetic-runtime.md)及[运行说明](docs/operations/2026-10-08-xianlixiu-synthetic-runbook.md)：原 Express/React 代码无凭据打包、隔离 `runsc` + 合成 SQLite、私有 TLS 会话读回、同库写者拒绝、快照恢复和实例停启；真实业务库/快麦会话没有迁入。
31. [两次 ECS 整机重启与数据库启动修复](docs/tests/2026-10-08-r2-ecs-full-reboot.md)：首轮发现新平台 PostgreSQL root+cap-drop 复启失败，保留原容器并用 UID70 同数据目录恢复；第二轮整机自动恢复、Stage 代际不变与旧站/A/B 回归通过。
32. [四项后续交付工作的现场进度](docs/tests/2026-10-08-four-step-development-progress.md)：迁移 021 显式构建失败/旧版保护、已上线只读项目事件与核源用量、纤莉秀合成 SQLite 版本交接测试及旧 PostgreSQL 停止对象清理；自动新提交、仓库选择和 ECS 新应用版本仍未完成。
33. [批准的 testA/testB GitHub Actions OIDC→真实同机 `runsc` 自动 Preview](docs/tests/2026-10-09-actions-oidc-ab-delivery.md)：不移动 Mac GitHub App PEM，短期仓库令牌仅内存核固定来源；A/B 成功、故意失败及恢复提交后，当前两者的内部 CA HTTPS 生产 Host 实际读回 active（A 代际 3、B 代际 1）。
34. [A/B OIDC 同机试点运行说明](docs/operations/2026-10-09-actions-oidc-ab-runbook.md)：精确源码身份、Host 挂载硬额/隔离 Builder、配额推迟与保留旧版的核验和停止范围。

## 当前可运行的范围

新目录已有四个独立入口：`cmd/api`（控制面）、`cmd/site-gateway`（内部 HTTPS 静态入口）、`cmd/source-worker`（GitHub App 收件后的固定源码处理）和 `cmd/site-cli`（Agent 命令行）。在**隔离 PostgreSQL、本机目录及受信 TLS 测试 Host** 下，Agent 的普通静态 HTML 可从源码上传走到受保护 Preview、Owner 生产晋升及真实 HTTP 正文回滚；固定 Git webhook 或 App 主动轮询可登记已核验来源。**仅批准的 A/B 公开 Vite 测试仓库**新增 GitHub Actions OIDC→自动 `runsc` 构建→私有 Preview→实际读回后自动生产切流；通用 Vite/Next.js 自动隔离构建仍未通过准入。指定的纤莉秀静态模板已通过操作员可信 fixture 路径与独立 ECS 的私有 Preview、公开无凭据 HTTP 生产别名实测，不能视为平台通用 Builder。

`console-web/` 是第五个入口：项目目录优先的 Vue3 开发者工作台，读取 `/v1/me`、真实项目、OIDC Git 状态、交付事件与已核源用量。2026-10-09 已按内部 CA、精确 `console.ecp.lingtong.com` Host 在 ECS 更新静态字节并验证 A/B 内部 HTTPS 生产链接（旧前端目录保留用于回退），外部 TLS HTML/JS 哈希与本机构建一致；浏览器身份仍使用当前页面内存中的短期 Bearer，**不冒充已接入 OIDC 的公网 Team 控制台**。本机开发与限制见[运行指南](docs/operations/local-development.md)。

**旧 ECS 站点和旧数据库没有卸载/清库。** 新平台仅在原 ECS 的独立目录、专用测试库及精确测试 Host 下运行；该机获用户批准新增独立网络命名空间/cgroupfs 的 Builder Docker daemon。早期 Mac-only App 私钥签发的临时试点 Token 已撤销；如今 A/B 指定公开仓库由 GitHub Actions OIDC 搭配 Job 内短期只读 `GITHUB_TOKEN` 做固定 SHA 核源，ECS 自动 `runsc` 构建并通过内部 CA 私有 Preview 真字节读回，随后精确 HTTPS 生产 Host A/B 均读回 active。App PEM 不进 ECS，Job Token 不持久化，**没有持续未知租户自动 Worker**。无正式 DNS/公网受信证书；A 旧 HTTP 模板可用，B 本轮只有内部 CA HTTPS 生产入口，HTTP:80 仍跳转旧 IP。A/B 有持久 Team 预算和项目资源**逻辑预留**；指定 A/B 新输出卷有 64 MiB 内存型硬额，但同机 Builder 仍缺通用节点物理容量 ACK、全局持久存储硬额、独立故障域、任意仓库自动 CD 与完整纤莉秀 Node/SQLite/真实快麦托管，不将本轮固定仓库的受控成功误作共享 SaaS 准入。
