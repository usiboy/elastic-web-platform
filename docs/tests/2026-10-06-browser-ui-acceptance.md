# 新平台工作台与纤莉秀公开模板：浏览器验收

**日期：2026-10-06。结论：已部署页面的静态模板交互与项目控制面信息符合当前“受控测试”口径；不符合产品规格中的 R1 自助部署/完整纤莉秀动态托管目标。** 对照[产品规格](../product/2026-10-05-vercel-style-product-spec.md)的北极星任务与页面合同，不以视觉展示代替实际运行能力。

## 测试入口与安全边界

使用 `deploy/ecs/browser_qa_proxy.py` 将两个**已部署到控制 ECS 的实际静态站点字节**分别映射到 Mac 回环 `127.0.0.1:8765`（控制台）与 `127.0.0.1:8766`（公开 HTTP 模板），不修改系统 DNS/hosts。控制 API 请求经受信测试 CA 校验内部 TLS、固定 Host/SNI 与 IP；浏览器只填写 `ewp_qa_a` / `ewp_qa_b` 占位令牌，桥接进程从受限 Owner 文件在内存中映射为各自令牌，不下发真实令牌到浏览器。桥接器默认只允许 GET/HEAD；唯一的 PUT 是 B 对已预留资源申请 1200m（Team 预算仅 1000m），由桥接器逐字段校验后转发，预期实际 API 返回 409，不改变任何预留。使用 `/usr/bin/python3` 校验证书：Homebrew Python 3.14 对当前内部测试 CA 的 Key Usage 扩展要求不同，会拒绝该测试证书；不存在跳过证书验证的降级。

## 浏览器交互结果

| 范围 | 实际操作与读回 |
|---|---|
| 工作台桌面与 390×844 移动 | 使用 A Owner 进入 Project，Overview 的生产别名 `active` 与 `http://p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com/` 链接仅在已观察时出现；Settings 读回 A 预算 1500m/4096/4096、项目预留 1000m/2048/2048 与余额，明确写“尚未配给运行时”。移动页面 `document.documentElement.scrollWidth === innerWidth`，Settings 表单、操作按钮可见。 |
| B 隔离与空态 | 断开 A 会话后用 B Owner 进入，列表只有 B 项目，Overview 显示“还没有生产版本”，不提供生产链接；Settings 只显示 B 预算 1000m/2048/1024、预留 500m/512/256。输入 1200m 提交后实际收到 409，错误提示“申请超过 Team 当前剩余额度”；页面版本仍 v1。 |
| 纤莉秀公开模板桌面与移动 | 从 ECS 原公开 HTTP 入口代理取得实际页面与 JS；五条 `DEMO-` 合成订单，筛选 `DEMO-CT` + 唯品会 JIT 后仅一条、缺口 11；“调拨建议”和“商品货盘”切换正常。移动端导航按钮打开菜单，整页不横向溢出，生产页不显示本机 CLI 按钮、登录或业务凭据表单。 |
| 静态字节与旧服务 | 本轮工作台 `index.html` SHA-256 `7b07d311148c474a98b6c8d11aa34afc97cb081b99ef404edd110a8b912bfd52`、JS SHA-256 `f860580bc6b74646963873f74a39af5d95910432a9454504254f0d4314aa2f83`、CSS SHA-256 `2e09137e39fb4bb7941be25146e30364af3ffd4df5e2d9bd396766427c69c0a8`；ECS 侧逐字节校验后原子切换 `index.html`，HTML/CSS/JS 各返回 200。真实 A/B 私有 Preview TLS、testA HTTP 生产 SHA、旧 IP HTTPS 401 的 opt-in Go race E2E 均通过；后续 Git 状态页的新摘要见[增量记录](2026-10-06-git-status-browser.md)。 |

Chrome Lighthouse 在回环映射、移动 Settings 的 Snapshot 模式下，工作台对比度调整前 Accessibility `94`，调整后 `100`，Best Practices `100`；公开模板 Accessibility `95`、Best Practices `100`、SEO `100`。公开模板小字号深色侧栏元素有轻微对比度不足（示例 `4.47:1`，要求 `4.5:1`）；工作台 SEO `60` 不作为内部控制台验收目标。代理域名不具有正式 DNS，Lighthouse 分数不是公网 HTTPS 上线证明。控制台中未绑定仓库、未部署别名的 GET 404 是正常空态响应，不是前端脚本错误。

本轮修复了资源表单 CPU 步进原先只允许 `100m` 整倍数、与 API 允许 `100m` 以上逐毫核申请不一致的问题；将步进调为 `1m`，并提高 Settings 描述、余额字段、标签和页脚的文字对比度。`npm test` 4 项、`npm run build`（`vue-tsc`）通过，浏览器最终读到新哈希资源、B 实际资源仍为 v1。`go test -race ./... -count=1` 在独立 PostgreSQL 下各包通过，`go vet ./...` 无诊断。

## 对照最初目标的差距

产品规格的 R1 预期是：用户在 UI **选择已获授权的仓库** → 平台在独立 Builder 自动构建 Vue/React → 真正 HTTPS Preview/Production、分支/PR 自动部署和回滚 → 可看日志、真实用量和域名状态。当前工作台只实现 Projects、Overview、Deployments、部分 Settings 与资源预留，仓库仍需手填 Installation/Repository ID；没有 Import 仓库列表、Build Logs、Domains、Usage、分支/PR 状态和自动构建/发布。纤莉秀页面只有公开合成数据静态交互，其真实 Node/Express、快麦只读/写调用、SQLite 单写者与备份均未运行于平台。下一批的可执行任务与外部准入见[差距收敛设计](../plans/2026-10-06-r1-r2-gap-closure.md)。
