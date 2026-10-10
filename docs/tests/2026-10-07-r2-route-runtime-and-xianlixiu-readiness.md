# R2 动态路由、故障回退及纤莉秀业务数据迁移前置验证

**日期：2026-10-07。状态：本机代码与隔离数据库验证，未部署到原 ECS 控制 API/网关，R1/R2 仍为 blocked。** 用户要求开发动态部署路由、实例管理和纤莉秀业务迁移，允许原 ECS 旧站将来退役。本段以原有旧站继续运行作为现场保护，不把合成 Next 探针当真实纤莉秀上线。

## 动态部署/路由合同

- 新增迁移 `020_runtime_pilot.sql`，在原 `site_deployments` 的 Team/Project/Deployment 归属之下记录内部 Node 运行端口、并发、请求期限与 `ready/failed`；由**内部操作员**登记健康的 `127.0.0.1` 端点，普通租户不能用 API 指定目标地址或访问任意 IP。没有签名动态制品和跨节点路由准入时，本表不属于正式 Runtime 部署合同。
- 现有别名 CAS 和不可变固定 Preview 可关联试点动态 Deployment；快照重新加载时检查本机健康状态并把前一健康静态 Bundle 保留为回退目标。HTTPS 网关先做原有 Host/TLS/Preview 会话验证，然后才转发 GET/POST 等动态请求；限制正文 1 MiB、后端地址为本机固定端口，剥离平台 `Authorization`、`__Host-ewp_preview` 与伪造的转发头，私有 Preview 强制 `no-store`。静态路径仍维持原有 GET/HEAD 约束。
- 请求流式复制并主动 Flush；每 Deployment 限定并发和请求时限。响应前后端故障或超时时，以数据库代际 CAS 将生产别名回退到前一已验证**静态**版本，写路由事件与审计记录；失败的动态固定 Preview 从后续快照中移除。测试覆盖跨 Team 拒绝、原静态版本内容与代际、回退后重载仍然指向旧版本、未授权私有资源 401、平台 Cookie 不流入用户代码、容器忙 503、超大请求 413、真实 TLS 流式响应先读到首段再读到末段，以及 SSRF 目标拒绝。
- 在独立 PostgreSQL 下 `go test -race ./... -count=1` 全包通过、`go vet ./...` 无诊断。迁移 020 **尚未作用于 ECS**；线上原静态 A/B 与旧应用未切换。

## 运行时实例管理的真实边界

Go 网关现有控制只限单进程的并发槽、请求期限、旧版路由回退和故障审计。此前[真实 ECS Node/Next 探针](2026-10-07-r2-node-next15-synthetic-probe.md)已验证 `runsc` 实例的 CPU/内存/PID 限额、一次性内部网络及卡住的 Node 请求后由操作员删除容器。**两者尚未接成**每请求超时即由独立 Runtime Worker 强杀实例并自动重启；当前网关没有 Docker socket，也不能凭模拟回调宣称运行时已自动杀停。热实例池、缩零、长连接边界、跨版本动态→动态回滚、真实用量采样和容量 ACK 仍缺。

## 纤莉秀原应用数据与会话

原应用已有快麦访问 Token 写入每会话私有 `0600` 配置、子进程 HOME 隔离，以及 SQLite `BEGIN IMMEDIATE` 业务事务。本次在**原应用仓库**增加启动前 SQLite 单写者锁：同一个数据库路径的第二版本拒绝启动；正常 SIGTERM 先停 HTTP、关会话与数据库再释放锁；异常崩溃留下锁时必须核实进程和原业务写入状态后手工处理，不能盲目删除。原旧版本尚不持此锁，所以切换前仍必须停旧写入实例、核对数据和会话目录。

另增加 SQLite 官方在线 `backup()` 生成私有目录快照、`PRAGMA integrity_check`、SHA-256 和非覆盖式恢复到**新的**数据库路径的运维命令。除小库单测外，在用户明确选择“只做合成数据”前曾用 Mac 原项目本地 `db/product-pallet.sqlite` 验证生成 302,542,848 字节快照并恢复到独立临时数据库，完整性与 `product_catalog`、`sales_daily` 记录数和源库一致；随后清理这两份**测试副本**，仅保留原数据库，未上传或打印业务数据。后续 ECS 联调只使用合成数据；此结果不是 ECS 旧站数据库备份，也不证明已具有云端恢复演练。

原应用验证：`npm run typecheck`、`npm test`（37 文件、260 项通过）、`npm run build` 通过；一次与 Go Race 并行的测试曾出现一项 HTTP `socket hang up`，单测及顺序全量重跑通过，未修改业务路由或以单次失败掩盖结果。真实快麦 Token、原 ECS SQLite、订单与仓库业务会话**未**复制到新平台，也没有将它们发到公网 HTTP。

按用户“只做合成数据”的后续选择，在 ECS 同一隔离 daemon 另运行 `probe_r2_synthetic_sqlite.sh`：新建并仅临时挂载 32 MiB `tmpfs` 卷，`runsc`/无网络/只读根/UID 1000/256 MiB 内存/0.5 CPU/PID 32 内启动带 `DEMO-ONLY` 唯一记录的 Node SQLite 写者。第二个实例对同一独占锁写入按预期拒绝；仍有写者时 `node:sqlite backup()` 形成完整性通过的一致快照；停旧写者并释放锁后，恢复到**另一**合成库，`PRAGMA integrity_check` 和记录读回成功。脚本退出卸载并删除合成卷，Builder 无残留容器，原 ECS PostgreSQL/旧站容器 ID 与宿主 `docker0` 均保持不变。脚本 SHA-256 `96e089e40c5c92817d8a2c3039e3269f8db5fc8ad4c3e693745c0f3946e9242f`。这仅证明本轮合成数据的沙箱锁与备份机制，**不**证明长期持久卷/真实业务迁移准入。

## 上线前剩余约束

当前同机 Builder 的 `PrivateNetwork` 内 Next 实例只可在其自身一次性 internal bridge 被访问，Go Gateway 的内部本机端点试点与它并未连通。需要受控跨 netns 传输和 Worker 实例生命周期、签名动态制品、按部署绑定合成数据持久卷并演练恢复、动态→动态回滚及公开受信 HTTPS。用户暂不提供正式 DNS、只授权合成数据联调，且 Builder/控制/运行仍同机；因此**绝不**把本机路由原型或 `runsc` 合成探针记作真实业务迁移/R1/R2 上线准入，也不在替代路径读回前停旧站。
