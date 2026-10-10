# 同机 Builder 真 Git→一次性作业→签名私有 Preview：A/B 受控试点

**日期：2026-10-07。结论：授权的 testA/testB 已在原 ECS 的独立网络命名空间 `runsc` Builder 完成一次从真实 GitHub App 核源、控制库作业仲裁、节点生成实际静态字节、试点签名验证到 Gateway 私有 HTTPS 正文读回的链路。** 这是由 root 操作员逐段触发的**同机固定来源试点**，不是开放给其他租户的自动构建或完整 SaaS；A 原 HTTP 生产别名未切换，B 没有生产域。此前首次第二 daemon 误伤 `docker0` 的事故、恢复和当前隔离 unit 见[网络运维记录](2026-10-07-original-ecs-builder-ab.md)。

## 短期 GitHub App 身份与真实来源

App 私钥只在 Mac。获得用户明确授权后，在 Mac 为 Repository ID `1404606559`（testA）和 `1404606859`（testB）分别向 GitHub 签发约一小时、单仓库 `contents:read`/`metadata:read` 的 Installation Token；每枚均通过 `GET /installation/repositories` 读回**恰好该一个 Repo**。Token 从 Mac 私有 `0600` 文件经 Workbench 加密传输放到 ECS `/data/ewp-builder-20261007/private/temporary-git-ab.json`（root 私有目录/文件），没有写入源码、命令行参数或浏览器，也没有上传 App PEM。Operator CLI 仅为固定的 A/B Team/Project 写入经 token 实证的 `verified` 绑定；一次性 Source Worker **不配置生产后缀、不自动晋升**，以 App 授权身份轮询并取回固定 Git commit/tree/blob，产生两条 `origin=app_poll`（不是伪称 webhook）的持久来源，二者先保持 `awaiting_isolated_build`、自动 Deployment=0：

| 固定来源 | commit SHA | 实取归档 SHA-256 |
|---|---|---|
| `usiboy/testA` | `e6062c7469d13712515dbf19474c851c044b753f` | `e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d` |
| `usiboy/testB` | `cfaa2f283f5ced67b85899919fe10a4a9f99d014` | `4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3` |

独立新平台 PostgreSQL 在迁移 017/018 前做 `pg_dump -Fc`，`pg_restore --list` 校验备份 `pre-migration18-builder-pilot.dump` SHA-256 `0fdaf50a98fe17288a90218cfc7bff2a639662edb40d07f16bfbba56142d6a38`；两条经核验来源完成、签发作业前再备份 `pre-signed-jobs.dump` SHA-256 `aed3bb5ce398525b22ad4ce59787e9dadec95b96485a0bf8ac765334d6c559b2`。旧产品数据库与 Docker daemon 未迁移。短期 Token 用完后两枚均向 GitHub `DELETE /installation/token` 撤销成功，Mac/ECS 临时文件删除；Worker 一次性退出。**已知例外：**首次适配误把 GitHub 实际 390 字节 Token 按 256 字节上限拒绝，在落盘前有两枚 A 单仓库 Token 签发但没有保留其字节，无法主动逐枚撤销；它们**未上传 ECS、未打印明文**，分别约于 2026-10-07 15:55/15:57 CST 自动过期，后续核对本机时间已超过 16:07 CST。上限随后改为 1024，补了失败路径已签发 Token 自动撤销的测试。不能把早期两枚描述成“主动撤销”。

## 同机隔离作业与实际字节

用户批准的原 ECS 新建 Builder daemon 独立 Unix socket/data-root/containerd namespace、`PrivateNetwork=yes`（只见 `lo`）、cgroupfs、默认 `runsc`；**旧 daemon 默认 `runc`、旧容器 ID 与宿主 `docker0` 保持不变**。Builder 只读输入为 App 核过的两份公开源码包，Node 22.22.0 Bookworm-slim 镜像 config SHA-256 `e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3`；受限试点只允许 A/B 两个固定摘要，`flock` 串行运行无网络构建、只读根、禁提权、无 capability、UID 1000、PID ≤64，A 上限 1 CPU/2 GiB、B 上限 2 CPU/3 GiB。输出 HTML/CSS/JS 与各自 GitHub Actions 成功制品逐字节一致。B 逻辑预留事前按版本由 500m/512/256 MiB 升为 2000m/3072/2048 MiB（Team 预算 2500m/4096/4096 MiB），状态仍是 `reserved_not_provisioned`；不把逻辑预留称为独占物理配给。

两项目分别通过控制库 `IssueBuildJob` 校验 Git 固定来源、尚未超期的 Team/Project 预留和 `expectedVersion=0`，取得不同的一次性 Job ID/nonce。**作业签发之后**重新跑 `runsc` Builder，旧输出的 mtime 不能拿来签新作业。试点 Ed25519 私钥仅在同一 ECS Builder 的 root 私有目录、**不进入容器/API/仓库**；控制面只读独立公开 PEM（SHA-256 `3558ab143cadac26e5198e79f72fafe86bba832cd6857775448df253b3fad450`）。按实际归档 SHA、CI 文件字节、Job nonce、固定 Commit、签名公钥和数据库来源逐项核对后，`AcceptSignedBuild` 仅将它们登记为私有 `preview_candidate`；完成后签名作业与记录分别为 2，复放相同 A 声明不新增 Deployment。

| Project | 作业后实际 `runsc` tar.gz SHA-256 | 签名 Git Deployment / Bundle 摘要 / 固定私有 Host |
|---|---|---|
| A | `55b89cf54260526cd991763a1b93f4e1de3d15999c256b77ee527506aabb815b` | `dep_f243de7290f4ae2f40c0bcaa626185f5` / `8da4d7188d8059651bed3acc16ebe60d0aac03d94d9fd9c8c8562977407ce243` / `d-f243de7290f4ae2f40c0bcaa626185f5.preview.ecp.lingtong.com` |
| B | `e059de6f60dc787e7d66256c6f9d014cde93bf396daa032a344edb53486ca244` | `dep_d207e6a36f7c27e58a5b3463e634c897` / `6f4212024f005a8bf0a3369380be6e753fd7c3d003ee6aa212fc545629765cad` / `d-d207e6a36f7c27e58a5b3463e634c897.preview.ecp.lingtong.com` |

真实 HTTPS 会话在两个新 Host 上读取首页及 JS SHA，与 CI 完全一致；匿名 401、A/B 跨 Team 会话签发 404、跨 Host Cookie 401；网关实际发送正文后，两条 `site_deployments.observed_state=preview_ready`。Git Push 仍是 `preview_candidate`，没有发生 `production_requested` 或 B 生产切流，A 原手工 HTTP 站点也仍为 active。旧 A/B 私有 Preview、testB 旧/新操作员 fixture、旧 IP HTTPS 401 的 opt-in `go test -race` 均通过。数据库当前迁移 018、`github_build_jobs completed=2`、签名记录=2、只有原 A 的一条非固定生产 Alias。

## 运行与仍未满足的 R1 门槛

Token 撤销后不再存在持续自动 Git 取件身份；签名私钥是同机 root 操作者使用的**试点密钥**，签名候选读回后已从 ECS Builder 私有目录删除，保留的仅是受限公开验证密钥、数据库签名记录及受保护试点证明文件。该密钥不能证明机器独立故障域或对未来陌生租户可信。受信 Builder 的输出命名卷尚无总磁盘硬限；缓存预填充阶段仅对两个授权公开源码运行 `npm ci --ignore-scripts`，工作负载实际构建才 `--network none`。试点没有通过恶意 npm 依赖、硬磁盘额度、邻居压力、多实例调度、变更回滚、正式 DNS/公开 TLS/真人 OAuth 等 R1 门槛，纤莉秀 Node/SQLite/真实快麦会话仍未托管。再次构建新 SHA 必须重新取得经授权的短期单仓库 Token、基于固定 Git 字节签发新作业及受控签名密钥；不能拿本次 Operator fixture 或已完成的 Job 冒充通用自动 CD。

**后续增量：**固定 A/B 的**新构建**已通过[输出卷 64 MiB 硬限及超额反例](2026-10-07-bounded-builder-output.md)。本文件上述两条已经完成的签名 Deployment 属于改造前历史记录，不能溯及既往称其通过硬限；持续自动化与独立故障域仍未完成。
