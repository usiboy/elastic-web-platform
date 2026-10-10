# Vercel 产品依据与下一轮设计提示词

**核对日期：2026-10-05。** 以下为公开官方产品文档可核对的行为；本平台采用相似用户工作流，具体实现、价格和服务等级均由本项目确定。

## 官方依据与本平台取舍

| 官方文档 | 已确认的产品行为 | 本平台决定 |
|---|---|---|
| [Deployments](https://vercel.com/docs/deployments) | Git、CLI/API 都可部署；每次部署有独立 URL，可检查构建和运行状态 | GitHub + Agent API 同用 Deployment 管线，而不是旧版固定服务登记 |
| [Git](https://vercel.com/docs/git) | 生产分支合并/推送可自动部署；其他分支可自动生成 Preview | R1 默认自动生产及分支预览，允许项目切换人工晋升 |
| [Generated URLs](https://vercel.com/docs/deployments/generated-urls) | 固定部署 URL、可移动分支 URL、生产 URL 是不同用途，固定 URL 受保留期约束 | 按 Deployment/Branch/Production 三种路由对象建模 |
| [Environments](https://vercel.com/docs/deployments/environments) | Local、Preview、Production；首次部署默认生产，Custom Env 可另建 | R1 首次成功可生产，R3 再做 Custom Environment |
| [Environment variables](https://vercel.com/docs/environment-variables) | Preview 分支变量覆写、变更仅影响后续部署 | R1 保存变量版本快照；生产密钥不流向 Preview |
| [Project configuration](https://vercel.com/docs/project-configuration) | 自动框架识别、可覆盖构建/路由设置 | 用框架适配器及本平台 Bundle manifest，不盲目兼容 `vercel.json` |
| [Build Output API](https://vercel.com/docs/build-output-api) | 构建输出定义静态、函数与路由供运行平台消费 | 自建版本化 Bundle；仅借鉴“构建与运行解耦”思想 |
| [Deployment Protection](https://vercel.com/docs/deployment-protection) | Preview/固定部署 URL 可配置保护，不等于应用自身登录 | R1 默认成员可看 Preview、生产域公开，保护覆盖全部资源 |
| [Functions](https://vercel.com/docs/functions) | 函数与框架/区域关联、可复用热实例，CPU/内存等可观测 | R2 单区域 Node+Next.js 受支持版本；不要把静态站点声称为全功能 Serverless |

用户提供的 `$0.007/$0.014/$0.028/$0.105` 构建价格、`10 GB` 和 `$0.06/GB` 流量示例只是**设计输入**，不是经过核实的 Vercel 当前价或本平台将执行的费率。全球边缘、百万 QPS、毫秒同步、零冷启动均属于未来目标，必须用指定供应商/节点实测。

## 结构化背景（提示词可引用）

1. **产品主线：**Team/Project→框架自动检测→Git/Agent Build→Deployment（唯一 URL）→Branch/Production alias→域名、日志、回滚、用量；导入首页而非管理员资源页。
2. **部署安全：**隔离构建是开放 Git/Agent 自助的先决条件，Preview 不注生产密钥，部署成功要能从 HTTPS Host 读回；第一台 ECS 可清场，但不使同机陌生代码自动安全。
3. **分层计算：**R1 SSG，R2 区域 Node SSR/API，R3 缓存/多区域，R4 V8 Edge；V8 Isolate 不等于容器，没有完整 Node.js API。ISR 必须有再生锁/缓存失效。
4. **用量归因：**Active CPU、预留内存、Build CPU、客户端/Origin 传输各自定义来源；WAF 清洗前事件不进入租户账单；匿名公开站点合法请求可计。预算近实时，账本最终去重且缺测未知。

## 递进提示词

### A：产品是否像网站平台

> 作为云平台产品负责人，阅读 `docs/product/2026-10-05-vercel-style-product-spec.md`，从首次导入 Git 仓库到上线、PR 预览、回滚，列出所有用户可见步骤和“页面/按钮依赖哪个真实后端状态”。找出任何需要手填 SHA/镜像、管理员逐版签审、模拟 URL 的步骤并说明如何移除。输出用户旅程、信息架构、首发支持矩阵和两租户验收，不讨论旧系统代码迁移。

### B：构建与部署合同

> 作为构建/交付架构师，基于产品规格设计 Team/Project/Build/Bundle/Deployment/Alias 模型、GitHub App 和 Agent 上传同一管线。说明相同 commit 不同环境为何是不同部署；明确首次生产、失败不切分支/生产别名、固定 URL 保留期、框架自动识别与 Next.js 未支持版本的报错。输出状态机、数据库唯一键、API 和故障演练，区分确认收件与网站真的可访问。

### C：安全与 ECS 重建

> 假设旧 ECS 可清场，但历史原站点可能仍在访问。给出只读盘点→备份恢复→冻结/停用→新部署→DNS 切流的可回退操作方案。区分控制面、构建面、区域运行面，设计陌生 npm 脚本的隔离与 PID/磁盘/超时/跨租户负面验证；只有一台 ECS 时明确哪些能力不能向陌生租户开放。不得默认清空盘或宣称可信固定样例等于生产准入。

### D：边缘与计量

> 在 R1/R2 的真实链路通过后，设计 WAF 后路由与计量、签名配置 ACK、域名所有权/DNS/证书分阶段生效，进一步评估 CDN、V8 Edge 与 ISR。列出 CPU/内存/Build/带宽的可追溯事件和跨区域预算误差窗；给出受清洗攻击、合法匿名、重复事件、采样丢失与缓存失效的正反测试。示例费率只作为试算输入，所有性能/价格结论标注数据来源和验证状态。

**统一审查问题：**用户能否从空仓库在不接触 K8s 的情况下真的得到网页？如果 Git 自动预览、失败保留旧版、生产域和回滚尚未实现，方案即使列出再多 Serverless 组件也不算 R1 完成。
