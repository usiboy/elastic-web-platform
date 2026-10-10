# 从 Git 到网站：全新产品规格

**状态：待确认的产品设计，2026-10-05。** 以 Vercel 的项目、Git、部署和域名体验为参考，构建本平台自己的产品和实现；不是 Vercel API 兼容或其源码复刻。技术边界见[架构](../architecture/2026-10-05-clean-slate-platform-architecture.md)。

## 1. 用户究竟购买什么

用户购买的是“网站持续交付与托管”，不是“调用平台计算服务”或“申请一台容器”。核心概念只有 **Team / Project / Deployment / Environment / Domain / Usage**。AI Agent 是第一类开发者：可以创建/关联项目、提交源码或 Git 提交、等待构建、读取失败原因与 Preview URL；用户可随时在界面接管、审阅和回滚。绫通是首个渠道和可选业务集成，平台账号与网站访问不以绫通在线为前提。

**北极星任务：**一个新租户在界面选择自己有权访问的 Git 仓库，看到识别出的框架、根目录、构建命令/输出目录（可编辑），点击 Deploy 后出现真实 HTTPS 站点；推送功能分支自动出现 Preview 和 PR 状态；合并到生产分支后生产域名指向新版本，故障时可回到旧健康版本。

| 旅程 | 用户动作 | 正确的平台反馈 | 验收结果 |
|---|---|---|---|
| 新建 | 选择 Team → Import Repository 或 Agent 发起 Deployment | 展示已授权仓库列表、框架检测结果、根目录/安装/构建/输出配置，确认后启动首个部署 | 无仓库/无构建器时明确说明，不能让用户手填 Git SHA 摘要才可创建项目 |
| 持续预览 | 非生产分支推送或 PR | 每个部署有唯一 URL；分支别名指向最新**成功**部署；检查/链接写回 PR，展示排队/构建/部署日志 | 两次提交的独立 URL 内容不同，失败构建不把分支别名切坏 |
| 发布 | 首次成功部署及后续生产分支提交 | 默认自动给项目生产域名分配已就绪版本；可配置“构建后先预演、人工晋升”策略 | 已就绪后 URL 真实可访问；请求 `202`/构建日志完成不等于已上线 |
| 日常运维 | 打开 Project Overview / Deployments / Settings / Domains / Observability / Usage | 显示当前生产版本、来源提交、预览链接、Build/Runtime logs、真实请求/用量、版本与回滚 | 不用运维节点面板代替项目主页；无数据显示未知/未部署 |
| 团队协作 | 邀请成员、配置 Preview Protection、环境变量与自定义域名 | Owner/Developer/Viewer 权限，Preview 可用平台账号保护，Production 默认公开且可选保护 | 匿名能访问公开生产，不能旁路私有 Preview；改变量只影响新部署 |
| AI Agent | 在 Project 级受限身份上调用 upload/deploy/status/logs/preview | 轮询或事件读取构建结果，失败有稳定错误码，可重复提交同一幂等请求 | 不给 Agent 永久管理员凭据，不允许它自报别人的 Team/Project |

## 2. 首发版本做什么

**可对真实租户开放的第一个产品版本（R1）：**独立身份与 Team/Project；GitHub 仓库选择/授权与 webhook 自动部署；Agent API/CLI 上传项目；自动识别并构建 Vite（Vue/React）及普通静态 HTML；单区域 HTTPS 域名、固定部署 URL/分支 URL/生产域名；首部署自动生产，分支自动 Preview；PR 状态回写、真实日志；生产域名绑定/撤销、原子回滚；Preview 默认成员可见；基本用量/配额与内部试算。用户无需知道 Kubernetes、镜像摘要、制品签名或服务操作授权。

**紧随其后的 R2：**按项目选择 Node.js 函数与 Next.js 的一个经过适配/实测的版本范围，支持 SSR/API 路由与流式响应，区域热实例与缩零策略可观测；不可把任意 Next.js 项目/版本都标成已支持。后续 R3 扩展 GitLab、Custom Environment、ISR/图片优化等；R4 才评估多区域/CDN 供应商、V8 Edge Functions、正式按量收费。R1 未支持功能在导入时给出明确原因与备选，不偷偷把 SSR 当静态导出。

**R1 明确不做：**独立能力商城、快麦 Provider、表格编辑器、通用脚本工作台、评分服务、人工逐版签审产品流程。构建结果由自动化策略准入，不以“让管理员代替开发者逐次审核”作为日常产品体验；不满足安全准入时则停止 R1 对陌生租户开放。

## 3. 页面与交互合同

| 页面 | 首屏核心 | 关键操作 |
|---|---|---|
| Team / Projects | 项目卡片：域名、最近生产部署、框架、更新时间与状态 | Import Git Repository、Deploy with Agent、选择 Team |
| Import Project | 仓库列表→框架/根目录检测→Build Settings→环境变量→确认 | 显式显示识别置信度和可覆写项；选择生产分支 |
| Project Overview | 生产 URL、最新成功部署、Git 提交、最近失败构建和 Preview | Visit、Redeploy、查看状态/日志/用量 |
| Deployments | 每次部署独立行：commit、环境、阶段、作者、耗时、URL | 筛选分支/环境、打开日志/制品清单、Promote/Rollback |
| Domains / Settings | 默认域、待验证自定义域、证书/解析状态、Preview Protection、成员/变量 | DNS TXT 所有权校验，变量按环境与分支覆盖 |
| Observability / Usage | 请求数、错误、延迟、构建时间与流量；来源和采集时间 | 按部署定位错误，下载有界脱敏日志，查看试算与额度 |

**空态同样是功能：**项目存在但首部署失败不展示“生产已运行”；`Build Ready` 与 `Domain Ready` 分开；URL 未就绪不生成假的可点击链接。移动端至少可查看项目/部署/日志摘要；复杂设置保持桌面可访问。

## 4. Git 与环境的产品策略

- 首次导入成功后创建默认生产部署；后续生产分支推送自动生成新生产部署并在就绪后指向生产域名。Owner 可切换为“构建后待批准晋升”；构建失败时旧生产继续服务。手动 Deploy/Redeploy 也有新部署 ID，与 Git 提交关联但不覆盖旧版本。
- 每次成功部署的 URL 固定指向这次部署，受保留期约束；分支 URL 永远指向最新成功版本；生产 URL 和自定义域指向当前已晋升生产版本。固定 URL、分支别名与域名在 UI 中分开显示。
- Project 变量区分 Development（本地）、Preview、Production；Preview 可有分支覆写；Custom Environment 后续再支持。Preview 不继承 Production Secret；公开构建变量与运行时秘密明确分开；变化只进入新部署。
- Preview Protection 是项目开关，默认仅 Team 成员可访问，含 JS/CSS/API 路径；也可选择公开预览供外部设计评审，明确风险及权限。生产域默认公开，**非当前生产版本**的固定部署 URL 默认仍受保护，避免历史版本泄露。

## 5. 用户验收故事（上线前逐条打勾）

1. A/B 两个不同 Team 各导入一套真实仓库：Vue 与 React 首次构建成功，各自默认生产域真实 HTTPS 返回对应内容；同名项目也不串数据。
2. A 的 Agent 提交两次：commit URL 内容分别固定，branch URL 最新成功；第三次故意构建失败，branch URL 和生产 URL 保持旧版；PR 页面可查构建状态与 Preview 链接。
3. 生产分支更新使 Production 自动切流，构建/路由故障则保留旧版；Rollback 在不重写制品的情况下恢复旧正文；域名/证书/路由状态分别可见。
4. 匿名可访问公开生产但不能访问私有 Preview；B 的令牌、URL、日志和用量不能读取 A；撤权立即阻断未过期会话及 Agent 身份。
5. 对恶意构建/依赖、越界请求、磁盘/PID/时限触发安全拒绝，受影响租户不能拖垮平台/邻居；用量缺测标记未知，不把 WAF 清洗的攻击流量计入租户试算。

**外部决策清单：**默认域名及 DNS/证书所有权、首发 GitHub App 管理主体、可用 ECS 与是否批准独立隔离构建/运行资源、Agent 直接上传单次容量上限、免费配额/预算、Preview 默认共享范围。未决定前可以开发合同与测试，但 R1 的上线门不得跳过。
