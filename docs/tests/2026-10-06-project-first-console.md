# P6 本机开发者工作台：项目优先、真实状态和角色空态

**性质：本机控制台增量，2026-10-06；不是已可向租户开放的网站平台。** 前端代码位于 `console-web/`，使用 Vue3、TypeScript、Vite 与小型 typed API 客户端。后端 `/v1/me` 仅从当前有效 Bearer 还原 Team、角色、项目/操作作用域与过期时间，不向浏览器返回令牌；项目、部署历史、生产别名、Git 绑定均读取持久 API。操作入口包括 Owner/Developer 创建项目、Agent/Owner 的受限静态包提交、Owner GitHub 双重授权、已就绪 Preview 的无 URL Token POST 交换以及带预期代际的生产晋升/回滚。

## 信息与身份边界

- 默认首屏是 **Projects 目录**，详情为 Overview / Deployments / Settings；原运维节点、评分服务、假 URL 与虚构 CPU/账单不在用户工作台。`not_deployed` 显示“尚未部署”，生产仅 `requested` 时显示“等待入口确认”，未核验域名时不提供可点击生产链接。
- 首段角色来自 `/v1/me`：Viewer 不见创建/上传/晋升按钮，Agent 只使用已授予的源码/部署操作。服务端仍逐请求独立验权限，前端隐藏按钮不充当安全边界。
- 本机开发暂通过 Team ID + 短期 Bearer 进入；令牌由前端 API 闭包保存在内存，输入后清空表单，断开或刷新不保留。未来独立 OIDC/联合登录和完整 Git 仓库选择器需要新阶段验收。HTTP 本机页面关闭浏览器 Preview；配置受信 HTTPS 后才允许用一次性码在 POST 正文建立 Host-only Session。

## 实际验证

`npm ci` 安装 86 个前端依赖；`npm test` 三条 API 合同测试通过（作用域 Authorization、生产 `requested` 不冒充 `active`、真实 404 空态与 503 不可用区分）；`npm run build` 的 `vue-tsc --noEmit` 与 Vite 生产构建通过。独立 Chrome/Playwright **模拟 API fixture** 的桌面 `1440×900` 中，Projects 目录加载两项目、项目详情显示两条部署及真实状态文案，无 JS 页面错误/横向溢出；手机 `390×844` 的 Viewer fixture 无创建/提交/晋升按钮、无横向溢出且正确显示“还没有生产版本”。浏览器截图只存在本机临时目录，没有把模拟项目当成真实租户上线证据。真实 PostgreSQL/API 正反例由 [静态纵向切片记录](2026-10-06-local-static-site-e2e.md) 覆盖，本 UI 还需在正式控制域、真正团队登录和两真实租户数据下复验。

新增 `/v1/me` 和 Project Deployment 分页的隔离库回归也随 `TEST_DATABASE_URL=<专用 PostgreSQL 16> go test -race ./... -count=1` **全部通过**，`go vet ./...` **退出 0**；OpenAPI 0.8.0 的 **24** 个路径、**130** 个 `$ref` 解析成功；项目自己的 **14** 份 Markdown 相对链接 **0** 处断链。测试后的 `web_test_*`/`route_test_*`/`pipeline_test_*` Schema 总残留 **0**。首次未排除 `node_modules/` 的粗略链接检查报告的是第三方包自身链接；限定到项目 `README.md` 和 `docs/` 后结果为上述 0 处断链。
