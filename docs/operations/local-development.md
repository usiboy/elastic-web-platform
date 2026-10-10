# 四进程本机静态站点：运行与当前边界

**状态：本机静态 HTML 真 TLS 纵向切片，以及两个操作员可信 GitHub/Vite 产物在新平台独立 ECS 数据库中的私有 Preview 实测；尚未获得共享 SaaS 准入。** 控制 API 接收 Team/Project、Agent 上传和签名 Git push；独立 `source-worker` 重新取固定 Git 字节；独立 HTTPS `site-gateway` 服务静态候选；Agent `site-cli` 可上传、查状态、验证私有 Preview，Owner 可显式晋升/回滚。真正的 GitHub App 用户自助授权、Vite/Next.js 隔离构建及正式公网 DNS/证书仍未完成。不接旧项目数据库/容器。参见[测试 ECS A/B 实测](../tests/2026-10-06-ecs-private-preview-ab.md)。

要求 Go 1.23+、PostgreSQL 16。准备一套**独立的测试库**；本机开发示例使用仅绑定回环的临时 Postgres 容器（选择自行信任的 PostgreSQL 镜像，并将 `<镜像>` 替换为其名称）：

```bash
docker run --rm --name ewp-dev-postgres -e POSTGRES_PASSWORD='<本机临时随机密码>' \
  -p 127.0.0.1:15432:5432 <镜像>
```

在另一个终端从 `elastic-web-platform/` 启动，所有明文 HTTP 仅允许回环地址，非回环监听需要成对配置 `TLS_CERT_FILE`/`TLS_KEY_FILE`：

```bash
export DATABASE_URL='postgres://postgres:<本机临时随机密码>@127.0.0.1:15432/postgres?sslmode=disable'
export BOOTSTRAP_TOKEN="$(openssl rand -hex 32)"
export SOURCE_STORE_DIR="$(mktemp -d)"  # 专用本机临时目录，不放进源码仓库
export ARTIFACT_STORE_DIR="$(mktemp -d)"  # 与源码仓库、SOURCE_STORE_DIR 分开
export SITE_PREVIEW_SUFFIX='preview.example.test'
export SITE_PRODUCTION_SUFFIX='sites.example.test'
go run ./cmd/api
```

本机当前版本通过版本/校验和账本依次应用 `db/migrations/001_identity_projects.sql` 至 `018_github_build_jobs.sql`，迁移 SQL 修改后拒绝以同一版本再次运行；**ECS 当前仍为 016 版**，签名交接/作业租约仅在隔离库测试，未启用真正 Builder。操作员 `POST /v1/teams` 首次仅返回一次 `ownerToken`；Owner 创建 Project 后可签发限该项目的 Agent 身份。要运行 `site-cli deploy/status/preview`，按需授予 `sources:upload`、`deployments:create`、`deployments:read`、`previews:read`；不要下发 Bootstrap/Owner Token。源码上传限制为 1 MiB 的 `application/gzip` tar.gz、最多 128 个常规文件/目录，不执行包内脚本，敏感 dotfile、Dockerfile、构建锁文件和 source map 不进入静态成品。普通租户的 Vite/Next.js 返回明确的构建门槛错误，不能被当作已发布页面；手工验证的操作员 fixture 是专用路径。

迁移 015 后，平台运维可在受限控制端通过 Bootstrap 身份 `PUT /v1/admin/teams/{teamId}/resource-budget` 设置 Team 逻辑预算，Owner 可通过 `PUT /v1/teams/{teamId}/projects/{projectId}/resources` 按 `expectedVersion` 申请或释放项目预留；Team `GET /v1/teams/{teamId}/resources` 可查看预算、总预留和余额。新 Team **默认没有预算**，项目预留此时返回 409；仅数据库仲裁并发超售，没有启动工作负载、硬限制或计量。严禁把 Bootstrap 凭据复制到工作台、网站容器或源代码。A/B 的实验性预算、备份和边界见[资源控制面验证](../tests/2026-10-06-resource-reservation-control.md)。

另开终端，用受信 SAN 覆盖实际 Preview/Production Host 的测试证书运行站点网关（证书私钥不放进源码仓库）：

```bash
export CONTROL_ORIGIN='https://console.preview.example.test'
export TLS_CERT_FILE='<受信站点证书路径>'
export TLS_KEY_FILE='<只对服务身份开放的私钥路径>'
go run ./cmd/site-gateway
```

网关只接受 HTTPS，启动时核实 PostgreSQL 路由和实际静态字节，后续按有限周期加载完整快照；载入失败时 readiness 降级并保留上一健康公共路由，私有 Preview 每次仍检查数据库中的会话/成员状态。控制 API 签发的 60 秒一次性交换码只在 Preview Host 的 POST 正文消费，成功后生成 Host-only、`Secure; HttpOnly` 的短期 Cookie。正式浏览器接入还需要拥有域名 DNS/受信证书与独立控制台登录流程，不能靠本机 `hosts` 条目把通配域名变成公网域名。

`site-cli` 使用 `EWP_TOKEN` 环境变量中的**项目作用域** Agent 凭据，示例调用形态如下（将占位符替换为控制 API 返回的 ID 和本机安全生成的 `.tar.gz`，不要在命令行直接写 Token）：

```bash
go run ./cmd/site-cli deploy --team <teamId> --project <projectId> \
  --archive <site.tar.gz> --request-id <固定重试ID>
go run ./cmd/site-cli status --team <teamId> --project <projectId> \
  --deployment <deploymentId>
go run ./cmd/site-cli preview --team <teamId> --project <projectId> \
  --deployment <deploymentId> --control-origin https://console.preview.example.test
```

`deploy` 返回 `candidate/not_deployed` 而非虚构上线；`preview` 通过实际 HTTPS 网关验证首页并仅输出状态/正文 SHA-256，不打印一次性交换码、Cookie 或平台令牌。该命令要求 Preview DNS/SAN/端口实际可达，不具备时只能运行隔离 TLS 集成测试。Owner 使用同一 CLI 的 `promote`/`rollback --expected-generation <代际>` 请求变更生产**期望**别名；只有网关真实返回对应静态正文后 `observedGeneration` 和 Project `active` 才更新。

开发者页面在新目录 `console-web/`；另开终端运行：

```bash
cd console-web
npm ci
npm run dev
```

开发模式只监听 `127.0.0.1`，Vite 将 `/v1` 代理至本机 `127.0.0.1:8097` 的控制 API。页面需输入 Team ID 与该 Team 已签发的短期角色令牌；令牌只保留在此页面内存，刷新即丢失，**不使用浏览器持久存储**。项目目录、Deployment 历史/游标、生产别名、Git 绑定、资源预留均从真实 API 获取。HTTP 开发页面没有受信 `CONTROL_ORIGIN`，浏览器 Preview 动作保持关闭；只有在用户自有的 HTTPS 控制域、可信 Preview DNS/证书都实际配置且前端设置 `VITE_PREVIEW_SUFFIX` 后，才允许通过不含 URL Token 的 POST 表单进入站点。配置 `VITE_SITE_SUFFIX` 只允许在 Alias 已读回且域名被运维核验后展示生产链接；仅受控的公开无凭据模板通过构建时 `VITE_SITE_SCHEME=http` 指向已核实的 HTTP 别名，这些开关**不代替**后端/浏览器身份授权。

GitHub App 配置为可选的一整组变量：`GITHUB_APP_ID`、`GITHUB_APP_PRIVATE_KEY_FILE`（服务身份可读的 0600 PEM 文件、放在源码目录外）、`GITHUB_WEBHOOK_SECRET`。只配置其中一部分会拒绝启动。原运维 `POST /v1/admin/teams/{teamId}/projects/{projectId}/github-binding` 仅供受控预置，不是用户自助入口。Owner 自行连接仓库还需 GitHub App 的 **Client ID/Client Secret** 和平台注册的精确 HTTPS 回调：

```bash
export GITHUB_OAUTH_CLIENT_ID='<GitHub App Client ID>'
export GITHUB_OAUTH_CLIENT_SECRET='<只在控制服务受保护环境中的 Client Secret>'
export GITHUB_OAUTH_CALLBACK_URL='https://<控制台受信域>/v1/oauth/github/callback'
export GITHUB_OAUTH_PKCE_KEY="$(openssl rand -hex 32)"
# OAuth 启用时控制 API 自身必须配对 TLS_CERT_FILE/TLS_KEY_FILE，直接以 HTTPS 接收回调。
```

Owner 以自身平台身份 `POST /v1/teams/{teamId}/projects/{projectId}/github-connect` 指定目标 Installation/Repository ID，服务端发 10 分钟随机 state 与 PKCE challenge；GitHub 回调时只凭**双侧证明**绑定：一次性 state 与未失权 Owner、用户令牌看到本 Repo `pull` 权限、App 安装短令牌实际能读该 Repo、两侧 Repo ID/名称一致。用户短期 OAuth/刷新 Token 不写入数据库，绑定只记录 GitHub 数字用户 ID 以供撤销事件匹配；默认仍须事先知道 Installation/Repo ID，**可选仓库列表的 UI 尚未实现**。GitHub App 用户授权 `github_app_authorization` 撤销、安装删除/暂停、仓库移除的已签 webhook 将绑定标记 `revoked` 并阻断待处理源码；原运维代绑 API 不会悄悄恢复撤销，Owner 须重新发起 GitHub 授权。

`POST /v1/hooks/github` 验原始字节 HMAC-SHA256，按 Delivery ID 去重；匹配已核验的仓库时登记 `pending_commit_verification`。配置 GitHub App 后，可另起 `go run ./cmd/source-worker`（需同一 `DATABASE_URL`、两份存储根路径、`SITE_PREVIEW_SUFFIX`、可选 `SITE_PRODUCTION_SUFFIX` 和只读 App 身份），自动以限定单仓库的短令牌重取 commit/tree/blob，核实际 Git 字节并对普通静态 HTML 建立受保护 Preview 候选；Vite 只进 `awaiting_isolated_build`。内网测试无法接收 GitHub webhook 时，可以显式设置 `EWP_GITHUB_POLL_INTERVAL`，由同一个 Worker 周期查询**已绑定且未撤销**的 App 仓库主分支，事件记录为 `origin=app_poll`；这不是 webhook 事件，也不绕过 Vite 构建门槛。主分支在 Preview 被网关真实读回、远端当前分支头仍指向此 SHA 后，才自动**申请**生产别名，不能以申请代替已观察的生产切流。Webhook 接入公网时必须使用可信 HTTPS/受控入口，GitHub App PEM 和 webhook 密钥不能挂到 Builder/网站工作负载。完整请求/响应见 [`openapi/platform.yaml`](../../openapi/platform.yaml)，不要把 Bootstrap/Owner/Agent/App 凭据打印到终端历史、日志或文档。

`internal/framework` 只读识别静态 HTML 与 Vite Vue/React（要求锁文件）；普通 HTML 使用固定、无需执行脚本的 `builtin/static-copy:v1`。`internal/routing` 将候选/固定 Host、别名 CAS 代际及审计写入 PostgreSQL，把不可变静态字节单独按摘要存于本机目录。生产发布请求成功只改变期望代际；站点网关加载后实际完整发送 `/index.html` 才记录该代际的观察态。独立 [本机真实 TLS 验证](../tests/2026-10-06-local-static-site-e2e.md)检查 A/B 来源隔离、Preview/生产正文与回滚，**不是多节点配置 ACK 或全球 CDN**。

隔离 Builder 尚未启用。`go run ./cmd/builder-probe` 可在候选独立构建节点上只读检查 Docker 是否声明 `runsc`；退出码 1 表示不能执行不可信 Vite，退出码 0 **也只说明运行时被发现**，还需要节点隔离、实际限额、固定镜像与可信产物准入。Vite preset 只接受精确 `vite build`，以后沙箱执行时应直接调用 `./node_modules/.bin/vite build`，不得用 `npm run build` 触发额外生命周期钩子；真实安全门槛见[构建前置准入记录](../tests/2026-10-06-builder-preflight-gate.md)。

隔离数据库集成测试：设置 `TEST_DATABASE_URL` 指向**专用测试 Postgres**，`go test -race ./...`；测试为每个用例创建独立 schema 并在完成后删除。未设置该变量时数据库测试会显式 `Skip`，此时测试通过不能表示数据库链路已验证。停止临时容器会删除其数据；不要给真实业务共用该实例。

**尚缺边界：**归档与静态产物仍用单机本地目录，没有多节点对象存储或租户累计硬磁盘额；不能向陌生租户开放上传。GitHub App 单仓库只读固定字节与主动轮询已对真实 `testA`/`testB` 验证；GitHub **用户 OAuth** 核验/撤销链仍仅在模拟 Provider API 下通过，未接真实用户授权或组织权限变化。无持久用户访问 Token 的方案仍需针对仓库协作者权限变化等非 OAuth 撤销事件补全持续对账/失败关闭。控制台暂为 Bearer 入口而非 OIDC/团队浏览器会话，尚无仓库可选列表、隔离 Vite/Node 构建、正式 DNS/受信证书、跨节点路由 ACK、故障域恢复与正式 WAF/计量。Bootstrap 令牌是受保护的运维引导凭据，不能交给浏览器/Agent；`/readyz=200` 仅表示控制数据库可达，不是网站上线准入结论。
