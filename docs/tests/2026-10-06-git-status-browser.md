# Git 来源阶段列表与项目工作台读回

**日期：2026-10-06。结论：项目用户现在能发现有界的 Git 事件状态；这不是构建日志或自动部署。** 现有 ECS 测试 A/B 没有在线已绑定的 Git Push，工作台诚实显示“尚无 Git 提交记录”；Mac 隔离测试库中真实 GitHub App 的 testA/testB 轮询仍是 `awaiting_isolated_build`。操作员可信 fixture 不出现在 Git 自动部署列表，也不被记为 Builder 产物。

## 实现和合同

- `GET /v1/teams/{teamId}/projects/{projectId}/github-pushes` 使用项目作用域认证，分页每次最多 20 条，按 `received_at` 与 Delivery ID 降序，并以不透明游标继续。跨 Team 为 404；缺 `projects:read` 权限的项目 Agent 不能枚举列表。响应返回真实来源 ref、固定 commit、`webhook`/`app_poll`、当前处理状态和可选 `errorCode`/`deploymentId`，不返回原始 webhook、Token 或源码字节。OpenAPI `0.10.0` 记录返回值和错误；迁移 `016_github_pushes_project_lookup.sql` 建立 Team/Project 查询索引。
- Overview 用真实状态区分“来源核验中”“等待隔离构建”“等待 Preview 读回”“等待生产读回”“处理失败/已取代”；仅数据库确有 Deployment 时显示 ID，等待 Builder 时明确说明“未创建自动部署或可访问的 Preview”。未绑定或未收到 Git 事件时显示真实空态，操作员制品单独列于部署历史。
- 隔离 PostgreSQL 的用例包含 A 项目 22 条同时间戳事件的两页稳定游标、异常游标 422、B 跨 Team 404、项目 Agent `projects:read` 可读和缺授权 Agent 404。Vue typed API 校验 Bearer 请求只发本 Team/Project 并保留 `awaiting_isolated_build` 而不虚构 Deployment；`go test -race ./... -count=1` 全包通过，`go vet ./...` 无诊断，`npm test` 5 项及带类型检查的构建通过。

## ECS 发布与浏览器复验

只在新平台专用 PostgreSQL 迁移 016 前备份 `pre-migration16.dump`，`pg_restore --list` 可读，SHA-256 `ec8f1a0d1a3499311ce426c8f806fb7de99a4e289962a822e8ac3e25e4bd1bdb`。Linux API binary SHA-256 `e9eaec4dac02b37889944de78d50b308b7bf48a87170cc3f214323509326ea48`，部署脚本保留原 binary 并在重启失败时恢复；迁移账本实测 16 条，匿名 `/v1/me` 仍 401。新工作台 HTML/CSS/JS 摘要分别为 `c57d7bfd5129697551a2c6cb40a7be004d2b91a19d7ecc6dda0fd91acffa04c9`、`8892cbfa79dd1f6d2beaf4c8c8b90f5704a3ae4e07cb14a6f92e1ff9b71e81a6`、`2e5d7b77fee22c5703739df56774686cb26a0d5e55ec5445f99df1ec618d943c`；上传后在 ECS 比对摘要、保存旧 `index.html`、原子替换入口，新 HTML/CSS/JS 各返回 200。

通过回环 QA 代理在浏览器读取 ECS 的实际 HTML/JS/API：A Owner 桌面 Overview 展示真实生产与“尚无 Git 提交记录”，B Owner 在 `390×844` 只看到 B 项目、未部署生产与同一诚实空态；移动无整页横向溢出。新增 Git 空态后首次移动 Lighthouse Accessibility 从原先 `100` 退到 `94`，修正旧版 Overview 的文字对比度后复测 **Accessibility 100 / Best Practices 100**（Snapshot，控制台回环映射，非公网 DNS 验收）。真实 ECS opt-in A/B 私有 Preview、新资源状态/跨租户 Git 列表 404、testA HTTP 模板 SHA 和旧 IP HTTPS 匿名 401 全部通过。

真实 GitHub App 轮询在 Mac 独立测试库中重新读取 `usiboy/testA` 固定 SHA `e6062c7469d13712515dbf19474c851c044b753f` 与 `usiboy/testB` 固定 SHA `9ea1fc0c7037315d7bda12e5b34ca3fafec2a3af`，两者仍是 `awaiting_isolated_build`、自动 Deployment `0`。由于没有独立 Builder、公网 OAuth/DNS，**本轮不对 ECS 人工伪造 Git 推送，也不把列表空态说成两租户真实自动发布已完成**。
