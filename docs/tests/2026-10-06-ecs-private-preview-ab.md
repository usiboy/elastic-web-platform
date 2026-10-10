# 旧控制 ECS 上新平台的 A/B 私有 Preview 验证

**日期：2026-10-06。结论：两个授权测试仓库的操作员可信静态制品已在新平台独立数据库和 HTTPS 入口完成私有 Preview 实测；不构成 R1 SaaS 上线准入。** 源码和受控构建字节的来源与差异见[真实 GitHub 与受控构建记录](2026-10-06-real-github-ab-build.md)。本次只使用用户指定的 `testA`、`testB`；没有运行任意租户的 Vite/npm 构建。

## 部署隔离与变更

- 在旧控制 ECS `i-bp19lwbwc5ej8pk0917s` 的独立 `/data/ewp-preview-20261006/` 下部署本项目专用 PostgreSQL（回环 `127.0.0.1:15434`）、控制 API（回环 `18097`）、站点 Gateway（回环 `18443`）和 Vue3 控制台。Nginx 新增精确测试 Host 的独立 vhost，`nginx -t` 通过后 reload；旧 `/` IP、原 80/443、18092/18093 及旧数据库未停用或迁入。
- `testA` 的固定私有 Host 是 `a.preview.ecp.lingtong.com`，Deployment `dep_0204843d32480c91047601ebdf2306e3`；`testB` 是 `b.preview.ecp.lingtong.com`，Deployment `dep_bd595e0b66f7873cd1d0403f0986ef27`。两者归独立 Team/Project，来自已按真实源码与构建归档 SHA-256 核验的**操作员可信 fixture**，而非平台自动批准的租户构建。
- 首次公网集成测试因两个 fixture 的 `desiredHost` 读回为空而失败；`BindFixed` 已在同一事务中回填 Deployment 元数据，新迁移 `014_fixture_host_readback.sql` 只为原有固定 Host 回填旧候选。专用 PostgreSQL 迁移前 `pg_dump -Fc` 保存于本项目 `state/pre-migration14.dump`，权限 `0600`，用隔离 `pg_restore --list` 验证；归档 SHA-256 `43b0264a35065465f9314f71ebef9bbcc925de1546ddaafa9a3c439c00858e43`。迁移器 binary SHA-256 `1ef65a2c9a7d7f86defc5b5ef28df76bcd8c920b14ad9504e2a2d1c044f4b774`；脚本 `deploy/ecs/reconcile_fixture_hosts.sh` 在专用 service 环境中幂等读回两个原 Deployment ID，退出码 0，未创建替代 Host。
- App 私钥、Token、证书私钥、数据库备份均未放入源码或公开输出；Github App 私钥未上传 ECS。短期测试 CA/叶证书保存在 Mac/ECS 各自受限目录，不是公网受信证书；测试叶证书到期时间 **2026-10-08 04:25:32 UTC**。

## 实测结果

在 Mac 上通过明确的 IP 连接重写（等价于仅对该客户端的精确 Host 解析）以及 `EWP_LIVE_ECS_CA_FILE` 指向私有测试 CA，运行 opt-in `TestLiveECSPrivateABHostsWithVerifiedTLSAndNoLegacyRegression`：

| 断言 | testA | testB |
|---|---|---|
| 自己的 Owner 令牌读取 `/v1/me`、项目和 Deployment，跨 Team 请求 | 200、跨 Team 404 | 200、跨 Team 404 |
| 匿名请求 Preview 首页 | 401 | 401 |
| Owner 一次性交换码、Host-only Secure/HttpOnly Cookie | 303，Cookie 属性正确 | 303，Cookie 属性正确 |
| 已授权 HTML SHA-256 | `3428587748ce2e568eff8e22ab8db33f20061df832180b915aa52778bbeb9419` | `b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a` |
| 已授权 JS 静态资源 | 61,532 字节，200 | 188,582 字节，200 |
| Gateway 实际读回后的 Deployment `observedState` | `preview_ready` | `preview_ready` |

两个 Host 的 TLS 均经**本轮明确加载的测试 CA** 验证。A 的 Cookie 到 B 请求得到 401；旧 `https://114.55.237.62/` 仍由系统信任根验证证书并返回匿名 401。控制台入口 HTTP 200、`/v1/me` 匿名 401。Mac `/etc/hosts` 未修改（当前自动化环境的 `sudo -n` 需要密码），也未配置正式 DNS；浏览器默认根证书不会自动信任本轮测试 CA。

独立 PostgreSQL 本地回归使用 `TEST_DATABASE_URL`：`go test -race ./... -count=1` 各 Go 包通过，`go vet ./...` 无诊断；迁移测试模拟旧版 Host 行和不应回填的可变 Alias，验证只修复固定 Host。ECS 集成测试显式设置 `EWP_LIVE_ECS_*` 文件路径与 Team/Project ID，`go test -v ./internal/integration -run '^TestLiveECSPrivateABHostsWithVerifiedTLSAndNoLegacyRegression$' -count=1` 通过。测试日志只打印 Host、静态字节摘要和 JS 大小，不打印一次性码、会话或令牌。

## 未通过的准入边界

`testB` 的默认 Rollup/Vite 转换仍不能在受控时限内完成；已服务的 B 产物使用保留 React 插件的临时 `treeshake=false` **诊断构建**。第二台 ECS 只有 `runc`，尚无不可信依赖执行的强隔离、硬磁盘配额与已验证签名供应链；成品和 PostgreSQL 是单机存储，没有跨节点 Gateway ACK 或恢复演练。新平台 ECS 未联调真实 GitHub 用户 OAuth/自动 Vite 构建；未测试生产发布、回滚、正式 DNS/公网受信证书或浏览器完整自助导入。旧控制 ECS 仍运行旧产品；上述两站只证明固定样例的私有 Preview 通道可用，不能把 `preview_ready` 表述为 Production 或整体达到 85%。
