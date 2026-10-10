# P2–P4 阶段合同：固定 Git 字节、静态制品与本机 HTTPS 路由

**性质：新项目局部工程验收记录，2026-10-05；不是线上 R1 站点。** 原 ECS 未修改，旧项目数据/实现未引入；`Project.status=not_deployed` 保持真实。

## P2 固定 Git SHA 的字节验证

`internal/source/github/pinned.go` 使用 GitHub App JWT 换取限定 Installation/Repository 的短期只读令牌，查固定完整 commit SHA 与 tree SHA，拒绝截断 tree、子模块/符号链接、非法路径、错误 blob 模式或超限文件；逐 blob 以 Git 对象格式复算 SHA-1，再打包排序且可复算的 tar.gz 并计算实际归档 SHA-256。运维身份在 `POST .../github-pushes/{deliveryId}:verify` 对已核验绑定/待核验 webhook 执行该操作，按 Team/Project 写本机受控对象及 `github_verified_sources`，复跑返回同一 Source ID，跨 Team 不可读取，项目仍未部署。

**验证口径：**`httptest` 充当 GitHub REST API（真实的 App JWT/单仓库 token 请求及多端点 HTTP 逻辑，**不是 GitHub 真实仓库或真人的 OAuth 安装验收**）；正例重取两次 tar.gz 字节一致，篡改 blob/SHA、符号链接和无效 commit 失败。新增事务迁移账本按版本记录 SQL SHA-256，已运行的迁移被改写时拒绝继续启动。

## P3 框架与静态 Bundle 合同

`internal/framework/Detect` 只读解析已验证的源码 tar.gz 与 `package.json`/npm 锁文件，识别 `static-html`、`vite-vue`、`vite-react`；Next.js、缺锁文件或畸形清单拒绝。识别框架并不执行用户 `build` 或 `postinstall`。`internal/bundle` 对已生成的静态文件逐字节拷贝、记录路径/hash/类型/总摘要并读回复算；只对普通 HTML 网站走 `builtin/static-copy:v1`，Vite 源码不可直接伪装为已构建的 `dist/`。本机 `DiskStore` 可按内容摘要持久读回与发现破坏，**不是已签名的隔离 Builder 或生产对象存储**。

## P4/P5 局部路由与回滚合同

`internal/routing` 在本机保存真实静态字节的固定 Deployment Host 和可 CAS 切换的 Host Alias；重复绑定固定 Host 到另一部署、过期代际切流、跨 Team Host 接管都拒绝。Gateway 必须走 HTTPS、核对 SNI/Host、按路径读同一部署实际文件；私有 Preview 的 HTML/JS 都经过鉴权回调并 `no-store`；生产 Alias 按 `one → two → one` 真 HTTP 正文回滚。测试使用本机临时 CA/覆盖两个 Host 的受信 TLS 证书，客户端验证证书/SNI；Preview 鉴权回调是**测试替身**。注册表在内存中、无持久路由/真实 DNS/公网站点/控制面调和，因此 `one → two → one` 是可证伪的本机合同，**不是 P4/P5 产品阶段通过**。

**同日后续：**路由意图、代际与恢复另有[单机持久化增量](2026-10-05-p5-durable-routes.md)；本段“无持久路由”是当时的阶段事实。后续仍没有真实公网 DNS/TLS、Gateway ACK 或生产可用性准入。

## 验证与剩余阻断

新测试按红→绿执行；隔离 PostgreSQL 16 使用新项目自己的临时容器，仅映射本机回环随机端口。`TEST_DATABASE_URL=<本机隔离测试库> go test -race ./... -count=1` **通过**：`cmd/api`、`internal/bundle`、`internal/framework`、`internal/httpapi`、`internal/routing`、`internal/source/github`、`internal/source/uploads` 均为 `ok`，`db/migrations` 无独立测试文件；`go vet ./...` **通过**。OpenAPI 0.3.0 YAML 可解析，共 **14** 个路径；**10** 份 Markdown 文档相对链接检查 **0** 处断链。测试 Schema `web_test_*` 清理后数量为 **0**，未触及旧平台数据库或 ECS。

真实 R1 尚需：GitHub 用户与 Team 仓库归属授权、固定源码自动队列、隔离 Builder 中真实 Vue/React 构建和恶意依赖负例、签名静态制品/对象存储、持久别名和路由配置 ACK、真实域名/证书、浏览器可用的 Preview 成员会话、预算计量与故障恢复。仅有单 ECS 不可把上述合同开放给陌生租户执行代码。
