# 原始网站平台目标差距收敛：从逻辑预留走向真实构建与动态运行

**日期：2026-10-06。依据：**[产品规格](../product/2026-10-05-vercel-style-product-spec.md)、[实施路线](2026-10-05-rebuild-roadmap.md)、[A/B 浏览器验收](../tests/2026-10-06-browser-ui-acceptance.md)及[纤莉秀模板记录](../tests/2026-10-06-xianlixiu-http-template-fit.md)。本文件规定剩余阶段的实现与验收次序；只有新平台独立服务、实际资源和可信身份通过实测后才能改写“blocked”。

## 当前基线与明确阻断

完成：独立 Team/Project 身份及作用域、真实 GitHub App 固定字节读回、受限 Agent 静态上传、操作员可信 A/B 私有 Preview、testA 合成数据静态模板的手动 HTTP 生产别名、Owner 手工晋升，以及持久 Team 预算/Project 预留并发超售 409。工作台桌面/移动可读回状态，但尚无完整 UI 导入旅程。以上**不是**两租户 R1 自助上线或纤莉秀真实业务上线。

| 依赖门 | 现有问题 | 可执行的下一段 / 退出证据 |
|---|---|---|
| 构建节点与容量池 | 第二 ECS 身份本轮不可用；Team 预算不能映射物理 CPU/内存/存储 | 查实可用的独立 Builder/运行节点与拥有者、实际规格和总容量，给每节点登记剩余可分配值。先做只读发现和授权容量基线，再允许为 Team 预算上限与 Project 预留做全局仲裁；不能用控制 ECS 共用余量假装独立容器。无节点时维持 `awaiting_isolated_build`。 |
| P3 隔离 Builder | [真实 testA/testB 新固定源码](../tests/2026-10-06-real-git-ab-ci-independent-vm.md)已通过 Git/CI/独立本机 VM `runc`；后续[ARM64 gVisor `runsc` 试验](../tests/2026-10-06-runsc-real-ab-handoff.md)再用真正沙箱重复构建 A/B、与 CI 内容字节一致、在本机隔离库用临时密钥把真实产物送入私有候选。B 原 Rollup 卡住由专属提交 `cfaa2f2` 修复，未设平台通用策略 | 在**常驻且经批准的独立云端节点**固定镜像/lockfile/Node 版本，限制 cgroup CPU+内存、PID、总写入磁盘、构建超时与出站域；禁用平台密钥挂载。节点私钥只留在受控 Builder；由控制面一次性作业绑定已批准节点/镜像与物理资源 ACK，Worker 才能自动领取固定 SHA 构建、校验签名 Bundle 后写入候选。A/B 在真正线上 Worker 再验，增加恶意依赖、磁盘、超时和邻居状态反例；本机 VM 与测试签名不充当 R1 准入。 |
| P2 用户 Git 导入 | App 有单仓库读取能力，但没有真人 GitHub 用户安装/选择列表 | 给 Owner 提供只读可选仓库接口并对仓库列表逐项核对 GitHub 用户权限 + App Installation，浏览器经受信 OAuth 回调完成选择，拒绝过期/失权/跨 Team 与手填其他 Repo ID；Webhook/轮询后 Git 主分支/PR 触发可信构建。公网回调及 App 主体先确定。 |
| P4/P5 HTTPS 域名、自动 CD | testA 只有无凭据 HTTP 手动别名；真实 DNS、外部受信证书和多节点 ACK 缺失 | 确认 DNS 所有权与证书机制，完成每部署固定 URL、分支 URL、生产域名。只在制品 SHA、网关目标版本 ACK、真实 HTML/JS TLS 读回后标记 READY/active。连续成功/失败/回滚三次真实提交，失败构建不切老站；PR 状态指向对应 Preview。旧 IP 401 回归必过。 |
| P6 UI/计量 | Settings 只显示逻辑预留；[Git 状态列表](../tests/2026-10-06-git-status-browser.md)现能发现待隔离构建/失败阶段，但无真实构建日志，仍缺 Import/Logs/Domains/Usage | 页面按资源状态区分`budgeted_not_provisioned`、`reserved_not_provisioned`、`provisioned`、`unknown`，没有 ACK 不能显示运行中。新增仓库选择、Build/Runtime 日志、真实请求/构建/磁盘/CPU 用量及缺测来源时间；A/B 按项目验收分页、隔离、空态/错误态与移动布局。 |
| R2 纤莉秀动态应用 | Node/Express、SQLite、`kuaimai-cli` 仅在原本机项目运行；公网 HTTP 不允许传递真实 accessToken | R1 隔离 Builder/可信 HTTPS 与运行节点完成后，以真实纤莉秀源码制作受控部署：服务端只读快麦会话放独立 Secret，不进浏览器/HTTP；写操作按业务授权；SQLite 每租户单写者持久卷，快照/恢复、关机恢复和峰值内存压测。部署/回滚时避免两个版本并发写同一 SQLite。验证 API 代理出站隔离、凭据不入日志、实例硬限/超额终止与原始业务用例。 |

## 完成定义

**2026-10-07 同机试点决策：**用户允许原控制 ECS 作为**指定 testA/testB 测试 Builder**。该机经过真实回归后，以仅在独立 network namespace 的第二 Docker daemon、独立 data-root/containerd namespace、cgroupfs 与 `runsc` 串行构建两个固定 Git 来源，实际字节和 CI 一致；见[同机实测与网络事故恢复](../tests/2026-10-07-original-ecs-builder-ab.md)。这替代了“在第二台 ECS 才能做 A/B 构建试验”的假设，**不自动免除**无输出磁盘硬额、旧站同机故障域、任务/签名自动交接、敌意负载邻居隔离和正式域名的 R1 上线门槛。

**同日签名交接增量：**用户还明确允许 ECS 短期保存两枚各限单仓库的只读 Installation Token；[受控真 Git→作业→私有 Preview](../tests/2026-10-07-same-ecs-git-signed-ab.md)已经在 ECS 对 A/B 各核验固定来源、串行构建、签名并读回，令牌用后撤销。仍须设计可持续且可撤权的 Git 用户授权/凭据续期与作业调度、节点真实磁盘硬额和签名私钥独立信任边界，才可从一次性操作员试点升级为 R1 自助自动 CD。

**后续硬限增量：**[固定 A/B 输出卷实测](../tests/2026-10-07-bounded-builder-output.md)以 `tmpfs` 限制每个新输出卷 64 MiB；真实 `runsc` 超额写入返回 `ENOSPC`，重跑两份固定 CI 字节成功，签名入口核对真实挂载。它只解决试点输出路径无界写入，不解决可信缓存准备/daemon 其它宿主写入、独立节点容量 ACK、自动调度和其它 R1 阻断。历史已签名 Preview 未重新签发，不把早期“缺输出限额”的记录反写成当时已满足。

**R1 继续推进的资源决定：**用户要求继续使用当前原 ECS 作为 Builder，暂不提供公网域名。此前同 ECS gVisor 固定试点仍可用于验证代码，但按[产品验收故事](../product/2026-10-05-vercel-style-product-spec.md)及[架构非协商门槛](../architecture/2026-10-05-clean-slate-platform-architecture.md)，同机故障域和缺公网受信 HTTPS 无法判定 R1 通过；不得通过改写百分比或把内部 CA/HTTP 称作正式上线来绕过。[本机作业超期失败增量](../tests/2026-10-07-r1-build-job-expiry.md)先解决一个自动交付缺口，线上迁移/Worker 尚未启用。

**R2 继续推进的资源决定与验证：**用户允许原 ECS 上未来退役旧站并做 R2 动态运行，但仍未提供域名。仅使用无凭据合成数据，已按[Node 22 / Next.js 15.5.27 真实 `runsc` 探针](../tests/2026-10-07-r2-node-next15-synthetic-probe.md)验证内部 SSR/API/流式响应与临时资源/网络清理；旧站此时并未清退。没有动态部署/函数路由、运行节点硬额 ACK、真实纤莉秀会话与 SQLite 持久性证据，不能越过 R1/R2 准入，也不能因用户许可移除旧站就立即删去原业务数据或容器。

**后续代码增量：**[动态路由与原应用数据边界验证](../tests/2026-10-07-r2-route-runtime-and-xianlixiu-readiness.md)新增迁移 020 本机试点，HTTPS Preview 鉴权之后转发动态请求、流式响应和代际 CAS 回退到已验证旧静态版本；原纤莉秀增加 SQLite 单写者锁和在线可校验备份/非覆盖恢复。本机代码合同尚无跨 netns Worker 连接、容器超时自动杀停和 ECS 真实业务迁移；前一段对正式 R2 阻断的描述仍然有效。

**同 ECS 合成闭环后续现场证据：**[R2 固定合成运行时与生产旧版回退](../tests/2026-10-07-r2-same-ecs-functional-loop.md)已用 root 专用 Unix relay 穿越隔离 netns，TLS 精确 Stage Host 真实提供 Next SSR/API/分块响应；故意挂起返回 504、Stage DB `2→3` 回退签名静态版本、reaper 杀停仅该沙箱，随后新签发 Deployment 在代际 `3→4` 再次就绪。原 A HTTP 生产别名和旧站不变。用户把**当前阶段目标**限定为“同机、无正式 DNS、只含合成数据的功能闭环”，此口径通过；正式多租户 R1/R2、安全故障域与真实纤莉秀业务仍不能据此视为已上线。

**2026-10-08 双动态回退增量：**[同 ECS v2→v3→v2 实测](../tests/2026-10-08-r2-dynamic-version-rollback.md)已补齐前述合成试点中的“上一健康版本是动态部署”场景。v2 与 v3 各有真实隔离容器及 Unix relay，Stage `4→5` 切 v3；挂起 504 后 Stage `5→6` 回 v2，v3 定时回收不误停 v2。独立 PostgreSQL 测试覆盖两版先后失败继续回已验证静态版本；宿主重启自动拉起与真实业务托管尚未验收。

**同日启动恢复增量：**[合成 R2 持久 systemd 启动链与演练](../tests/2026-10-08-r2-runtime-recovery.md)确认旧 API/网关原为重启即失效的 transient unit、独立 Postgres Docker 容器原 `restart=no`；现控制 API、**新命名** Gateway、Next 容器恢复 unit、Unix relay 皆为 enabled 的持久 unit，新平台 PostgreSQL 调整为 `unless-stopped`。只停/启这条 R2 链得到同一容器 ID、同一 Stage Deployment/代际与旧站回归；未进行整机重启，因此真正宿主开机及其它服务竞争仍属待验，不得由此宣称正式 SLA。

**原应用合成迁移增量：**[纤莉秀 Express/React + 合成 SQLite 私有路径](../tests/2026-10-08-xianlixiu-synthetic-runtime.md)从原项目代码生成无真实数据库/会话的受核包，在 ECS `runsc` 内以原 SQLite 仓储初始化合成库、在线快照/恢复、验证同库第二写者拒绝；经平台 Owner 私有 Preview 实读原 UI 与健康/API。`EWP_SYNTHETIC_ONLY=1` 在进入快麦认证器前拒绝凭据提交，旧站、A 原生产、Next Stage 不变。当前用户只授权合成数据，不能把这段成功解释为真实快麦订单/库存业务或原项目完整生产迁移。

**两次真实整机重启：**用户两次授权[全 ECS 重启验证](../tests/2026-10-08-r2-ecs-full-reboot.md)。首轮暴露新平台 PostgreSQL root 入口和 `--cap-drop ALL` 与 UID70 数据目录不兼容，API/Gateway 因数据库不可用重试，旧受限纤莉秀探针保持健康；保留旧容器和受限备份、以 UID70 同 PGDATA 启动替代容器后，单独数据库容器复启通过。第二轮整机启动数据库及 API/Gateway 自动恢复，Next Stage 原 Deployment/代际 6、原应用合成 SQLite 私有站、A/B 和旧探针实际读回通过。此项为**同机计划维护重启**验收，不证明真实业务凭据迁移、突发断电、独立故障域或共享租户 SaaS 准入。

R1 必须用两个真实 Team 从零经**仓库选择→平台自动 Vite 构建→私有 Preview→公网受信 HTTPS 生产→自动后续提交→回滚**跑通；日志/计量/硬限与 A/B 隔离正反测试均有原始证据。R2 在此之上再验真实纤莉秀 Node/SQLite/CLI 的受控身份与持久性；只要独立节点、DNS/证书或业务数据安全入口缺任一项，就只能保留静态合成模板，并明确记录 blocked，不能通过前端重画或逻辑预算代替。
