# P2 增量：Owner GitHub 用户授权、仓库双侧归属与撤销

**性质：代码/隔离 PostgreSQL + 模拟 GitHub OAuth/REST 正反例。** 尚无真实 App PEM/Client Secret/公网 HTTPS 回调，不能标记为真实两租户仓库自助 Import 上线。

## 授权合同

GitHub 官方[用户代行访问边界](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user)规定：用户令牌只可访问**用户与 App 都有权**访问的资源。[安装 API](https://docs.github.com/en/rest/apps/installations#list-repositories-accessible-to-the-user-access-token)提供 `/user/installations/{id}/repositories` 逐仓库 `permissions.pull`；[Web OAuth 流程](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)支持随机 state、PKCE 和 Repository ID。

本轮 `POST .../github-connect` 仅 Team Owner 可申请。`github_oauth_states` 只存一次性 state 的 SHA-256、Team/Project/Owner Token ID、Installation/Repository ID 和 10 分钟有效期；PKCE verifier 由服务端密钥与 state 派生，数据库不保存原值。只有直接 HTTPS `GET /v1/oauth/github/callback` 可消费回调：重验 Owner 令牌/角色，交换短期用户令牌，查询 `/user` 数字 ID 和安装内仓库读权限，再由独立的单仓库 App 安装令牌复核 Repo ID/全名，事务内置 `verified` 与审计。短期用户访问/刷新 Token 只在请求内存中使用，不写数据库/响应或日志。

GitHub App [授权撤销、安装及仓库授权变化事件](https://docs.github.com/en/webhooks/webhook-events-and-payloads#github_app_authorization)需先通过原始字节 HMAC-SHA256：用户撤销按 GitHub 数字用户 ID 将对应 OAuth 绑定置 `revoked`；无可靠用户身份/ghost 时保守撤销全部 OAuth 来源绑定；安装删除/暂停或仓库移除撤销对应安装/Repo；已排队未发布的来源标 `failed/github_binding_revoked`，新 push 不入队。相同 delivery 幂等、不同正文冲突。已运行站点不因授权撤销被无审计地立即删库/切流；实际网站停用政策需独立确认。

## 正反例与剩余边界

测试覆盖 Owner 成功绑定并继续接收签名 push、跨 Team Owner 与 Viewer 拒绝、GitHub 用户 `pull=false` 拒绝、Owner 令牌撤权/10 分钟 state 过期/重放/非 TLS 回调拒绝、恶意授权 URL 无 PKCE、App 与用户读范围独立校验、授权撤销后操作员旧 API 不得绕过、Owner 重新 OAuth 后恢复、安装删除阻断待处理任务及 ghost 身份时保守撤销。部署历史新提供 20 条游标分页，同名异 Team 和 Viewer/Agent 作用域均以真实数据库测试。模拟 GitHub 服务并非账号所有者真实授权：后续仍要在正式 App 与两个独立 GitHub 用户/Team 中核对安装选择、组织 SSO、仓库协作者权限变化、webhook 漏送对账和证书/DNS；不能据本次 fixture 把 P2 升格成生产准入。

**实际结果：**在新项目独立临时 PostgreSQL 16 上执行 `TEST_DATABASE_URL=<隔离实例> go test -race ./... -count=1`，`cmd/api`、`cmd/site-cli`、`cmd/site-gateway`、`cmd/source-worker`、`internal/bundle`、`internal/framework`、`internal/httpapi`、`internal/pipeline`、`internal/routing`、`internal/source/github`、`internal/source/uploads` 全部返回 `ok`；`db/migrations` 无单独测试文件，迁移在隔离数据库集成用例中执行。`go vet ./...` 退出 0；`gofmt -l .`、`go mod tidy -diff` 无差异；OpenAPI 0.7.0 YAML 可解析，**23** 个路径、**129** 个 `$ref` 均解析；**13** 份 Markdown 相对链接检查 **0** 处断链；临时 `web_test_*`/`route_test_*`/`pipeline_test_*` Schema 残留数 **0**。GitHub HTTP 成绩仅对应模拟 Provider，不是实际安装账号已验收。
