# 纤莉秀模板与网站平台适配、HTTP 发布和资源准入记录

**日期：2026-10-06。验收范围：仅用户授权的公开 `usiboy/testA` 与新平台测试租户。** 原纤莉秀项目 `/Users/liumingjian/Development/workbench/纤莉秀` 保持原 Git 工作区不变；原项目的 Express、`kuaimai-cli` 会话、SQLite 与内部部署文件未复制到公开仓库。这里先将三类业务流程改造为**无真实数据/凭据的 Vue 静态应用模板**，再以真实源码、CI、平台可信候选和 HTTP 站点逐段核验。

## 已验证的实际链路

1. 原纤莉秀项目：`npm test` **35 个文件、257 项测试通过**；`npm run typecheck`、`npm run build` 通过。原项目自己仍是服务端调用 `kuaimai-cli` 的动态应用，不是纯静态网站。
2. testA：保留旧 Git 提交历史，正常推送新 `main` 提交 [`e6062c7469d13712515dbf19474c851c044b753f`](https://github.com/usiboy/testA/commit/e6062c7469d13712515dbf19474c851c044b753f)。旧倍数计算页已替换为订单缺口、库存建议与商品货盘模板；页面只有 `DEMO-` 合成样例。`npm test` 两项通过，`npm run build` 通过；Chrome 验证桌面/移动布局、订单筛选与切换工具。开发页仅在本机显示 CLI 按钮；浏览器经本机只读桥接实际查询 **31 家店铺**，页面只显示数量。没有请求实际订单、写入库存或输出店铺详情。
3. [GitHub Actions 构建](https://github.com/usiboy/testA/actions/runs/37422492357)：Node 22 的 `npm ci --ignore-scripts`、`npm test`、`npm run build` 均成功；7 天无凭据静态制品已下载，与本机三个输出文件 **逐字节一致**。`index.html` SHA-256 `e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6`，JS `78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a`，CSS `e1e8f6dd498f7017c7033bf78947a1fa907ede06cedbe10de3de842cf0be1531`。
4. 新平台真实 GitHub App：逐仓库只读令牌重新获取 testA 固定 Git tree/blob，实际来源归档 SHA-256 `e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d`（13 个文件，`vite-vue`）。App 主分支轮询见 `origin=app_poll`；testA 新 SHA 与 testB 旧 SHA 均停在 `awaiting_isolated_build`，自动生成的 Deployment 仍为 **0**。这是平台自动构建尚未获准的真实状态。
5. 单独的**操作员可信 fixture**：以 CI 已核验静态文件打包（归档 SHA-256 `dc93eddd2391fdc06e2511f47eb35b3db69ade4c5c0c6deacf26ef662fe6c2a3`），只在新平台测试租户导入为 `dep_b94c2c1f169b1aad4f40ab835f2f3146`，固定私有 Host `a-e6062c7.preview.ecp.lingtong.com`；Bundle 摘要 `d35c83f381541848ba5b19c367dd18fc80c4a1393d00c08a6cb4e2a937f4ca08`。操作员声明不是签名的隔离 Builder 来源证明。迁入前对**新平台独立 PostgreSQL** 做受限 `pg_dump -Fc` 并验证归档，SHA-256 `c1fd52917afa0a83c9504b52a2b7355de322fdd52b64cb78eb242b2a516a0b1b`。
6. 私有 Preview 实际读回与 Owner 显式晋升后，新平台生产别名 `p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com` 达到 `active`。公网 **HTTP**（无公开 SSL）入口通过端到端测试，匿名读到上述 CI 首页 SHA 和 74,249 字节 JS；`/api/shops`、`/v1/me`、`/session/exchange` 均 404，POST 不可写，私有 Preview 不经 HTTP 暴露。旧 IP HTTPS 仍是系统根验证通过且匿名 401，B 租户私有 Preview 及 A 原固定 Host 的实测回归亦通过。生产 Host 的 Nginx 使用锚定正则避免默认 `server_names_hash_bucket_size=64` 对长 Project ID Host 的配置错误。
7. HTTP 入口的 Nginx→网关仍走本机回环 TLS；只轮换**新平台内部测试 CA/叶证书**，新叶证书到期 **2027-01-04 06:29:23 UTC**。首次轮换因 Nginx reload 后立即读回旧 worker 证书而自动恢复原配置；加入按真实 TLS/HTTP 响应轮询后，第二次轮换通过 `control=401`、`public_http=200`。CA 私钥只在 Mac 受限临时目录，未上传 ECS；旧 IP 公网证书未改动。内部证书没有自动续期，正式长期运行前需确定保管和轮换机制。

回归：专用本机 PostgreSQL 下 `go test -race ./... -count=1` 各包通过，`go vet ./...` 无诊断；两个 ECS opt-in 集成测试（旧 A/B 私有 Preview、testA HTTP 生产别名）均通过。`python3 -m unittest discover -s deploy/ecs -p 'test_*.py'` 3 项通过，覆盖打包产物字节稳定、输出权限及拒绝符号链接。GitHub App 真实轮询 testA 新提交与 testB 旧提交通过，Vite 来源均保持构建阻断。

访问时本地需把 `p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com` 解析为 `114.55.237.62`；或用 `curl --resolve p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com:80:114.55.237.62 http://p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com/`。当前没有正式 DNS，Mac `hosts` 未被自动修改。HTTP 站点只展示合成数据；原项目使用的 accessToken 会话仍不经公网 HTTP 提交。

## 对“通过资源管理平台开发、发布、CI/CD、资源分配”的逐项判断

| 能力 | 当前实证 | 当前判断 / 完整验收条件 |
|---|---|---|
| 开发模板 | testA 已替换，Vite Vue 静态交互、本机 `kuaimai-cli` 只读桥接、原项目实测通过 | **模板可用**；原 Express 订单/库存和 SQLite 数据分析功能尚未托管，不能称完整纤莉秀迁移。 |
| Git 与 CI | App 固定 SHA/字节复核；GitHub Actions 测试、构建、制品上传成功 | **CI 可用**；Actions artifact 是操作员核验输入，不是平台 Builder 自动准入/签名。 |
| 发布与回滚 | 固定私有 Preview 经实读，Owner 显式生产晋升与 HTTP 别名 `active`；平台已有代际 CAS/回滚代码与本机测试 | **指定样例手工发布通过**；尚需自动从 Git 生成可信 Vite Deployment、生产切流/回滚 E2E 及失败后保持旧版本。 |
| 动态运行 | 原项目本机 Node/Express、`kuaimai-cli`、SQLite 均能测试/构建；新平台只服务静态 Bundle | **阻断**：需要单租户 Node 运行时、只读/写接口范围和凭据安全入口、每租户 SQLite 持久卷及单写者/备份恢复实测。公网 HTTP 不承载 accessToken 登录。 |
| 构建资源 | testA CI+Mac 静态文件一致；旧二号 ECS 本轮 Workbench `InstanceId` 无效，杭州列表只有控制 ECS | **阻断**：无法把已完成的 GitHub CI/操作员 fixture 算作平台独立 Builder；需可用的独立构建节点、固定镜像/依赖、CPU/内存/PID/磁盘硬限、超时及产物签名/验证。二号 ECS 是否迁往其他地域或已释放未确认。 |
| 配额、计量与资源分配 | 来源归档压缩限 1 MiB、静态制品压缩限 16 MiB/单文件 4 MiB；Team/Project 数据隔离已测；后续增加 Team 预算、Project 持久预留及并发超售 409，A/B 测试租户已读回，详见[资源预留验证](2026-10-06-resource-reservation-control.md) | **部分控制面可用，实际配给阻断**：尚无全局节点容量池、实际 CPU/内存/持久存储硬限、用量采样/回收；数据库预留和静态文件大小限制均不能冒充运行时资源管理。 |
| 浏览器自助与域名 | 测试租户独立身份，HTTP 公共别名可用 | **阻断**：正式 DNS、GitHub OAuth 真人安装/仓库选择、可信生产域管理和生产事故恢复尚未验收。 |

**结论：**目前符合“单一授权租户的无凭据静态应用模板、GitHub CI、操作员手工发布与实验性资源预留控制面验证”这一窄口径；尚不符合“完整纤莉秀项目在平台开发、自动 CI/CD、动态运行和实际资源分配管理”的准入条件。下一阶段先以同一原项目验证隔离 Node/SQLite/CLI 运行、发布代际和资源硬配额，再讨论开放真实业务会话的可信访问方式。

**后续静态 Builder 实验补充：**独立 Mac 实验 VM 安装官方 ARM64 gVisor 后，真实 testA 固定 Git 源与已修复的 testB 固定 Git 源都在 `runsc` 下重复构建、逐字节匹配各自 CI 制品；本机隔离库只用临时密钥核验作业/签名候选。详见[后续验证](2026-10-06-runsc-real-ab-handoff.md)。ECS 在线 B Preview 仍是操作员 fixture，纤莉秀 Node/SQLite/快麦会话未托管，不能因此把上表动态运行或资源硬限项标为完成。
