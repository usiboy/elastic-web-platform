# R2 合成动态→动态回退：同 ECS 双实例真实验收

**日期：2026-10-08。范围：**沿用用户批准的同 ECS、内部 CA/精确 Host 和合成数据试点，不移动真实纤莉秀 SQLite/快麦会话，不改变原 A HTTP 生产别名或旧站。结论：新动态版本失败后，Stage 已真实回退仍在运行的上一动态版本；只清理新失败实例，旧实例继续服务。

## 两版同时存活及安全切流

1. Go 持久路由不再假设前一个部署只能是静态 Bundle。加载当前运行时快照时读取该 Host 最近最多 32 条路由代际，逐条验证 Team/Project、制品或仍健康的动态端点，保留有界旧版本列表；超时回退通过数据库当前代际 CAS 从候选里选最近健康版本。若新旧两个动态版本先后失败，跳过标记为 failed/不健康的版本，继续向更早的已核静态 Bundle 回退。内存快照同步应用数据库实际选中的版本，失败的固定私有 Preview 关闭。本机独立 PostgreSQL + `go test -race` 测试覆盖 v0 静态→v1 动态→v2 动态、v2 超时回 v1、网关重载后代际仍在、v1 再失联回 v0 及两个失败状态持久化。
2. 原 ECS 旧 v2 合成 Next 实例 `ewp-r2-next15-loop-20261007` 和 `/run/ewp-r2-20261007/runtime.sock` 均保持运行。新 v3 standalone 归档 SHA-256 `8668dfa8e229e7305b286b85122513651107631447125d78ddf9b307b80c6e78`，2343 个条目均通过路径/无符号链接检查；它只在固定合成 `/api/hello` 多返回 `variant=v3`。v3 使用同一个 Builder 专属 `--internal` bridge 上的另一个 `runsc` 容器 `ewp-r2-next15-v3-20261008`、固定 `172.29.77.3:3047`、独立 root-only Socket `v3.sock`，CPU 1/内存 768 MiB/PID ≤64/只读根/禁提权；relay 二进制只准接受 v2/v3 两组精确 Socket-IP:Port，网关无 Docker socket。
3. 网站网关在变更前为现有 v2 数据库及旧二进制做受限备份；`pre-r2-multiversion-20261008.dump` 经 `pg_restore --list` 验证，SHA-256 `bbcb6ced6d37d42c9ee074848943c6693b644dd7d57d71fcbfc40a45c423b65b`。新网关二进制 SHA-256 `0949a16be3059e5ee8ed17cb9c9fdaa627807dbe5b68a16d763ac22955cc1829` 经真实 Stage API 200、旧 IP 401、旧容器 ID/`docker0` 不变后接替；没有追加 ECS 数据库迁移，仍为 20。旧 v2 Stage 和私有固定 Host 在升级后回归通过。
4. 内部操作员对固定 A Team/Project、受核新摘要、`v3.sock` 登记新 Deployment `dep_0a8092587d1798386092cda3cccffc14`。私有固定 Host `d-0a8092587d1798386092cda3cccffc14.preview.ecp.lingtong.com` 的匿名401、跨 Team 交换码拒绝、Owner 会话下 SSR/API/流式回读通过；Stage 当时仍返回无 `variant` 的 v2。设置只回收 v3 容器/relay、绝不删除 v2 或共享网络的 `ewp-r2-v3-reaper.timer` 后，Stage 数据库代际 `4→5` 指向 v3，公网 IP + 内部 CA TLS 实际读到 `variant=v3` 及分块间隔；v2 的原固定私有 Host 仍可独立读回。

## 故障反例与当前状态

显式对**仅合成** Stage v3 的 `/api/hang` 发起一次请求，网关返回 504；数据库 `site_runtime_pilots` 标记 v3 为 `failed`，Stage 代际 `5→6` 回退 `dep_be15038c6bc73cd168e2b2ed967c6f11`，再次 GET API 返回旧 v2（无 `variant=v3`），v3 固定 Preview 为 404。独立 v3 定时回收器实际终止 v3 容器及 v3 relay；检查 v2 容器仍 Up、v2 relay active、internal bridge 未删除、v2 Stage SSR/API/流式读回通过。旧 A HTTP 生产 alias 仍是 `dep_b94c2c1f169b1aad4f40ab835f2f3146`、代际 1，旧 PostgreSQL/纤莉秀容器 ID 与宿主 `docker0=172.17.0.1/16` 未变。

本机独立 PostgreSQL 的 `go test -race ./... -count=1` 全包通过，`go vet ./...` 无诊断；Mac `npm run build && npm run test:runtime`（含运行时 variant=v3）通过。A/B 私有 Preview、原 HTTP 生产、旧 IP 401、本轮旧 v2 与新 v3 固定 Preview、Stage 实际超时/回退经 ECS opt-in `go test -race` 验证。

**阶段边界：**这是固定双合成版本的同机功能验证。再次启动时仍由操作员用固定来源包签发新 Deployment；没有通用项目自动构建/签名、宿主重启后的动态实例自动拉起/持久卷、完整运行用量与真实纤莉秀业务。正式 DNS 与独立故障域按照用户当前阶段口径暂不作为功能验证门，但不因此宣称已开放共享 SaaS。操作/退出步骤见[试点运行手册](../operations/2026-10-07-r2-synthetic-loop-runbook.md)。
