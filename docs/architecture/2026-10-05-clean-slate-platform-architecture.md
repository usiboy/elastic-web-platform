# 从 Deployment 出发的全新平台架构

**性质：技术目标设计，2026-10-05；未部署。** 对应[产品规格](../product/2026-10-05-vercel-style-product-spec.md)与[实施路线](../plans/2026-10-05-rebuild-roadmap.md)。旧项目的代码、数据库、API、镜像、队列、控制台、集群清单均不作为依赖，旧 ECS 只作为可清场后重分配的计算资源。

## 1. 设计原则与基石对象

**Deployment 是网站的版本单位，而非“注册一个受信服务”。** 每次 Git push、CLI/API 上传或 Redeploy 产生一个独立 Deployment Attempt；经框架适配器构建出不可变 Output Bundle（静态资产/函数清单/路由规则），验证成功后才提供固定 URL。Project 的生产域与分支域只是可以切换的 Alias，Rollback 指回旧的可用 Deployment。拒绝“创建修订→等人工签审→登记服务→短 JSON 调用”的旧模型。

```text
Team → Project → Source（Git Repository | Agent Upload）
              └─ Build Request(commit/config/env snapshot) → Build Attempt
                     └─ Output Bundle(static, functions, routes, provenance)
                           └─ Deployment(immutable URL, environment, health)
                                      ├─ Branch Alias → latest READY
                                      ├─ Production Alias → active READY
                                      └─ Custom Domain → verified alias
```

唯一键约束包含 `(teamId,projectId,requestId,requestDigest)`、`(provider,deliveryId)`、`(teamId,projectId,aliasName,generation)`、`normalizedHostname` 全局唯一；`commitSha` 相同但环境变量/框架/构建设置变化可产生新 Build 和 Deployment，故 **commit 不能充当 Deployment ID**。异步 API 创建返回 `202` 和状态查询地址；仅观察到路由/内容与健康读回后标 `READY`/`ACTIVE`。

## 2. 控制、构建、交付平面

```text
   浏览器 / AI Agent / 绫通服务端             GitHub Webhook
                 │                              │ 验签/二次拉取
                 ▼                              ▼
   [Product API + 控制台] ────────── [Source Gateway]
          │  身份/RBAC/Project                    │
          ├─ PostgreSQL: 事务状态+outbox ── [Build Scheduler] ── [隔离 Builder]
          │  项目、构建、部署、Alias、审计       │                │
          │                                    │     Output Bundle/签名/扫描
          └─ [Deploy Controller] ◀─────────────┘                │
                    │                              [对象存储 + OCI 仓库]
                    └─ 已就绪制品+Alias 版本下发 ──────────────┘
                                     ▼
   DNS/TLS → WAF → [Site Gateway/CDN] → 静态对象源站
                         └─ 区域 Serverless Node Runtime（R2）
                                     │
                         清洗后 Usage Events → 账本/配额/试算
```

| 平面 | 最小职责 | 状态与失败行为 |
|---|---|---|
| 产品控制面 | Team/Project、身份、Git 安装、构建配置、环境变量、域名、Deployment、Alias、用量可视化 | Go 服务+PostgreSQL；事务 outbox/Worker 领任务；DB 不可用时拒绝新增部署，不让站点入口依赖每次 DB 查询 |
| 构建面 | 固定来源取回、框架识别/锁文件、受限安装/构建、规范化 Bundle、日志、证明 | 独立沙箱/最小身份；按提交和配置确定性输出，失败不会覆盖已有 URL/别名；构建器没有签发私钥 |
| 交付面 | 域名 TLS、Host→Alias/Deployment、WAF、静态/函数分流、请求观测 | 路由签名快照+单调代际、last-known-good/ACK/读回；切流不依赖浏览器按钮的 200；路由与控制 API 不同 Host |
| 计量面 | 有界前置预算，清洗后记录并去重用量、费率版本、缺测 | 限额与最终账本分离；Redis/内存只是辅助缓存，不能单独承担财务账本 |

**基础选型：**Go 控制服务、PostgreSQL、版本化对象存储、OCI registry（R2）、隔离 Builder/Runtime Worker、现成 HTTPS 网关/CDN 或区域 Nginx/Envoy。初期持久队列可用 PostgreSQL outbox 与 lease/`SKIP LOCKED`，不可用数据库表直接扛百万 QPS 请求日志。Build Output 是**本平台定义的**版本化 manifest（静态文件路径/hash、函数入口/runtime/资源、路由与缓存配置），借鉴 Vercel Build Output API 的“构建输出与执行解耦”思想，**不宣称与 `.vercel/output` 兼容**。

### 首发 Bundle v1

```json
{
  "schemaVersion": 1,
  "projectId": "p1",
  "source": {"kind": "git", "commitSha": "<full-sha>", "archiveSha256": "<actual-bytes-sha256>"},
  "framework": "vite",
  "assets": [{"path": "/index.html", "sha256": "<actual-content-hash>", "contentType": "text/html"}],
  "routes": [{"path": "/*", "kind": "static", "fallback": "/index.html"}],
  "functions": [],
  "builderDigest": "sha256:<pinned-toolchain>"
}
```

字段示意不是可直接提交的 manifest；服务端需验证 schema、规范路径、总量、hash、签名和租户归属；R2 增加函数资源与区域字段并提供迁移兼容检查。构建缓存按输入 hash（源码、锁文件、适配器、环境版本）命中可复用字节，缓存不共享秘密；部署 ID 仍独立生成。

## 3. 核心状态与 Git/Agent 输入

1. 用户从 GitHub App 授权仓库列表中选择 repository ID，平台读取真实 manifest/锁文件检测 Vite/静态站点并提供默认配置；未识别则展示可改配置，不能盲目构建；根目录、构建命令、输出目录均保存为版本化 Project Setting。Webhook 原始内容验签、去重，再以限定仓库短令牌重新读取 commit SHA；不信正文自报仓库/分支。Fork PR 默认不注入变量且需授权策略，防构建窃密。
2. Agent 上传经短期限定 Team/Project 的上传 URL，按文件 hash/尺寸/路径等校验后创建部署；上传端不必拥有 Git 权限。API/CLI 查询、等待、取日志、取 Preview、晋升生产都使用相同部署实体和幂等语义；产物已上传不代表已执行或就绪。
3. `Build queued → fetching → detecting → building → packaging → verified | failed | cancelled | unknown`；`Deployment created → provisioning → testing → ready → active | expired`。失败、未知、取消不发生产/分支别名。旧成功版本保留。构建日志增量持久写入、SSE/游标可续读、脱敏且限流。
4. 第一次成功部署在项目生产分支（或首次显式部署）按照产品策略成为生产默认版本；之后生产分支推送自动切 Production，非主分支生成 Preview。允许配置 staged/manual 策略。静态 Bundle 先登记对象和路由，真实 `Host` HTTP 探测通过再将 Production/Branch Alias 的 `observedGeneration` 原子推进；不拿“Pod Ready”代替网站实际请求。发布失败则保留旧 Alias，回滚生成新的 Alias 代际指向旧可核验版本。固定 URL 永不复用其他产物，过期后返回 410。
5. Preview 默认受 Team 成员访问保护，授权覆盖 HTML、JS、CSS、API 与路由中间件；选择公开须明示，静态内容如果可被公开/CDN 缓存才允许共享缓存。生产域名默认公开；项目级可选全域保护。域名要验证所有权、DNS 指向与证书状态，不能因为 Alias 已生效就显示公网可访问。

**环境：**Local/Development 供本地开发，Preview 用于非生产分支，Production 绑定生产分支；Custom Env 在 R3。构建和运行秘密经密钥服务 envelope 加密，按环境及 Preview 分支版本化；Preview 默认值 < 分支覆写，Production 不继承到 Preview；变量更改只进入新部署，已运行版本保持快照。公开构建变量进 JS 包需明显提示；平台不得下发全局绫通/云账号密钥。

## 4. AI Agent 与绫通的独立性

AI Agent 调用的核心接口（拟建）：`POST /v1/teams/{team}/projects`、`POST /v1/projects/{project}/deployments`（来源为 Git 精确 commit 或上传会话）、`GET /v1/deployments/{id}`、`GET /v1/deployments/{id}/events`、`POST /v1/projects/{id}/aliases/production:promote`、`GET /v1/projects/{id}/usage`。Agent 身份限定 team/project/actions/environment，API 返回 `202 {deploymentId, statusUrl}`，结果就绪后查询到真实 `url`；不发平台 Operator Token。设置或删除域名、Secret、成员是更高权限操作。OpenAPI/API 作为产品合同，CLI/MCP 如需集成 AI 工具只是这些接口的薄封装。

绫通服务端适配 `(绫通 tenant,user,role,plan)` 到平台 `(teamId,member,planId)`，调用受限 provisioning API 创建/挂起 Team、成员、套餐；用户进入平台可通过受控身份联合/跳转（需会话验证、不可把上游 bearer 透传给网站）。平台也可直接注册非绫通租户，用户的网站请求走自己项目域，**绫通业务接口是用户网站按需申请的外部集成，不是发布或健康检查的前置条件**。绫通失联时既有网站流量继续走缓存/已签路由；套餐变更采用有界最终一致/失效安全的额度刷新。

## 5. 算力分层、度量与安全门

| 版本 | 计算与路由 | 应答承诺 |
|---|---|---|
| R1 | 单区域静态 CDN/源站，框架限 Vue/React Vite 与静态 HTML；带 SPA fallback、cache-control 与静态资源指纹 | 真实 Git/Agent → 隔离构建 → Preview/Production HTTPS；建站主流程已通 |
| R2 | 单区域 Node Functions + 受支持 Next.js SSR/API adapter，流式响应；热实例有限并发、进程池与 DB 连接池有界 | 函数无完整 Edge 零冷启动承诺；用户代码在专用沙箱执行，超时真实杀停 |
| R3 | 多环境、GitLab、ISR/图像优化按测试引入；CDN 供应商接入/多区域静态复制 | 缓存失效/失配与故障读回证据 |
| R4 | V8 Edge Functions + 多区域区域函数；部署限流与跨区额度分配、正式计量计费 | Edge 无全量 Node API；多区域与全球延迟根据实测，非单 ECS 承诺 |

域名请求路径：DNS/TLS → WAF/攻击清洗 → 域名/路径匹配、Preview Protection → quota → 静态对象或已绑定函数 → 响应字节/执行时间日志。公开站点合法匿名请求仍可计量；WAF 清洗拒绝的流量只写平台安全事件，不收租户 CDN/函数费用。Build CPU 按固定规格 vCPU × 可审计运行分钟；Active CPU、预留内存 GB-hours、静态响应和回源字节各自计量，不以应用 Pod 请求量替代 CPU 时间。事件有全局去重键和源 UID，失败/迟到/缺测写 `unknown` 或补偿，不把缺测计成 0。初期只提供内部试算与硬预算，费率示例需经业务定价，不复制 Vercel 公开价格。

**R1 自助开放的非协商门槛：**构建脚本和依赖执行是任意代码；构建 Worker 必须有隔离运行时/故障域与硬 CPU、内存、PID、磁盘、时限、网络出站额度；禁 Docker socket、hostPath、共享敏感缓存与控制面凭据；真实测试恶意安装脚本、fork/超磁盘/超时后进程未停、跨租户网络与邻居健康。R2 还须对长连接/动态运行再验。现有 ECS 可清空重装控制面/入口，但**只用同一台 ECS 的 namespace、Docker 容器或一套 K3s 不能自动证明陌生租户构建安全**；如无第二台独立工作节点或受验证的隔离构建服务，R1 开放入口保持关闭。

## 6. 可观测、失败回退与容量路线

观测关联 `team/project/source/commit → buildId → bundleDigest → deploymentId → aliasGeneration → Host/routeVersion → requestId`，错误能从项目页面和 Agent API 查到失败阶段、日志与建议操作。Gateway 采用签名路由代际、ACK + last-known-good；发布必须先读回真实 URL/目标探测，再 CAS 切 alias；部分节点 ACK 缺失则 `reconcile_required`，不谎报全网就绪。域名所有权、DNS、证书和路由传播分开显示。备份恢复需要 PostgreSQL、制品、签名/密钥引用和路由，恢复测试使用独立故障域。只用一台 ECS 时不声称高可用/全球边缘/百万 QPS。

**所需资源决策：**既有 ECS 清场后优先用作受控控制面+单区域入口（需复查规格/预算）；另配隔离构建 Worker，R2 再配独立运行 Worker；可用已验证的第三方沙箱/受控云资源代替自建 Worker，但仍需网络/存储/身份隔离验证。对象存储、DB、签名材料与域名/证书由所有者确定；设计不自动购买、迁移、卸载或删除云端资源。
