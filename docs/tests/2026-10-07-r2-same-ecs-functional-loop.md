# R2 阶段性功能闭环：同 ECS 合成 Next.js 经网关发布、失败回退与重建

**日期：2026-10-07。阶段验收口径：用户暂不要求正式 DNS 和独立故障域，选择同 ECS、内部测试 CA/精确 Host 与纯合成数据。** 在此口径下，受保护 Preview→合成 Stage 晋升→SSR/API/流式响应→故障时旧静态版本恢复→失败实例回收→新 Deployment 再发布已通过。它不代表正式 R1/R2 共享 SaaS 准入，也不是原纤莉秀快麦真实业务的迁移。

## 已读回的实际链路

1. 本机 `Next.js 15.5.27` + Node `22.22.0` 的锁定合成 standalone 包加 `/api/health` 与故意挂住的 `/api/hang`，无业务 Token/真实 SQLite。v2 归档 SHA-256 `a40af88a426cd65227352c1675425717f15393294e8685a6a62dbee8d42fe4c1`，验证归档仅 2343 个无符号链接/路径逃逸条目；本机 `npm run build && npm run test:runtime` 通过。
2. 原 ECS Builder 第二 daemon 保持 `PrivateNetwork=yes`。仅在这个 daemon 的 netns 内创建 `--internal` 的 `172.29.77.0/24` 网络，真实 `runsc` Next 容器无出站网络、只读根、无 capability、UID 1000、CPU 1、内存 768 MiB、PID ≤64。root 专用 relay 经 `nsenter` 进入该 netns，只为 `172.29.77.2:3027` 提供 `/run/ewp-r2-20261007/runtime.sock`（目录 `0700`、Socket `0600`）；网关无 Docker socket，外部只通过 Nginx 的 TLS 精确 Host 进入。`--network host` 在 gVisor 内不可用，已按现场失败证据改用独立 internal bridge，宿主 `docker0` 未受影响。
3. 原新平台 PostgreSQL 迁移前 `pg_dump -Fc` 备份 `pre-migration20-r2-loop.dump`、经 `pg_restore --list` 核验，SHA-256 `c3435880f2dc3aa2cd2c6b0be651c8cfbd1efa222d9d718969d768599991a645`。网站网关备份旧二进制 `ewp-site-gateway-linux-amd64.pre-r2-loop`，上线新网关 SHA-256 `b2b60736f193c8d94a05a58e4c2a0ccb2f129254a7d01d94465261fead2ea98d`、迁移数 20。首次验证脚本误用默认 `8098` 而现场监听 `18443`，另一次在 systemd active 后立即探测遇到监听启动竞态，两次均自动恢复旧网关；核对真实端口并按实际 TLS 401 轮询后升级通过。没有改旧站容器/默认 daemon。
4. 固定 A 项目先在**全新合成 Stage 别名** `r2-stage.preview.ecp.lingtong.com` 绑定已签静态 Deployment `dep_f243de7290f4ae2f40c0bcaa626185f5`，真实 HTTPS 首页 SHA-256 `e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6`。原 HTTP 生产别名一直指向旧 `dep_b94c2c1f169b1aad4f40ab835f2f3146`，没有被这轮 R2 改写。
5. Next v1 经 root 内部操作员登记为 `dep_fae3b413a77f3334325cef72f332378c`，固定私有 Preview 匿名 401、跨 Team 交换码 404，A Owner 单次交换 Cookie 后 API 200、两次 SSR UUID 不同、流式返回两段。Stage 代际 `1→2` 晋升 Next，经公网 IP 的内部 CA TLS 真实流式读回。初期 Nginx 默认缓冲导致首段迟到；只给短 Stage Host 新增精确匹配、`proxy_buffering off`，长固定 Preview Host 保持原保护配置。尝试给长固定 Host 加精确 `server_name` 时触发 Nginx 默认 64 字节 hash 桶限制，`nginx -t` 拒绝并自动撤销新配置；短 Host 的配置最终 `nginx -t`、旧 IP 401 均通过。
6. **故障反例：**显式向 Stage `/api/hang` 发请求，得到 504；数据库 Alias 代际 `2→3`，Stage 恢复前一静态 HTML 正文/摘要，失败固定 Preview 变 404，`site_runtime_pilots.state=failed`。2 秒定时回收器只对本合成容器/relay/internal bridge 执行清理，Docker daemon 无残留容器与测试网络。随后重新启动同一合成归档但**重新登记独立 Deployment** `dep_be15038c6bc73cd168e2b2ed967c6f11`，私有 Preview 再次真实读回，Stage `3→4` 晋升，当前动态 SSR/API/流式响应再通过；定时回收器只处理最新候选的失败状态，不误清理新实例。旧 A 公共 HTTP 别名仍为代际 1。
7. Mac opt-in `go test -race` 对本轮 R2 私有 Preview、Stage 分块时序、一次性故障回退，以及旧 A/B 私有 Preview、testA 公共 HTTP、旧 IP 401 共完成回归；有独立 PostgreSQL 的 `go test -race ./... -count=1` 全包通过、`go vet ./...` 无诊断。`nginx -t`、默认 Docker `docker0=172.17.0.1/16`、旧 PostgreSQL 容器 ID `7085d10db80f` 和旧站容器 ID `3034af04913a` 均未变化。试点私钥和临时 Git Token 文件仍不存在。

## 合成 SQLite 与当前工作范围

另一份**只含 `DEMO-ONLY`** 的 Node `runsc` 探针在 32 MiB 有界 tmpfs 卷中证明同路径第二写者被拒绝、在线 `node:sqlite backup()` 可形成完整快照、停旧写者后恢复到新数据库并读回；脚本退出删除合成卷。这是同宿主重启**容器**期间的数据保留/回读试验，不是宿主重启后的持久卷、真实用户数据或快麦 CLI 会话迁移。原应用代码增加的单写者锁与备份/非覆盖恢复在原项目测试通过；用户明确不授权把真实业务数据库传入 ECS，本轮没有这样做。实际旧站仍在运行。

**本轮“功能闭环”仅指固定合成 Next/Node 和已核静态旧版本之间的内部验证。** 后续要称真正的 R2 产品，还需通用动态制品签名/仓库自动构建、真实多版本动态→动态回滚、宿主重启后自动恢复、正式持久配额/日志用量、按租户真实业务授权。当前 Stage 若 ECS 重启且运行时未恢复，网关会核实际健康并回退到旧静态版本，不会宣称动态仍然在线。运维与退出路径见[受控运行说明](../operations/2026-10-07-r2-synthetic-loop-runbook.md)。

**2026-10-08 后续验证：**上述当时仍缺的动态→动态回退已在[双实例现场记录](2026-10-08-r2-dynamic-version-rollback.md)通过。Stage 当前代际更新为 6、v3 failed/v2 ready；本段原代际 4 的状态只描述 2026-10-07 当时现场，正式多租户与宿主重启准入仍未完成。
