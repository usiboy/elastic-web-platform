# P2 来源收件增量：Agent 归档与 GitHub App webhook

**范围：2026-10-05 本机新平台**；这是 Project 级来源输入和待核验事件，不包含源码执行、可访问网站或 ECS 变更。

## Agent 上传

`internal/source/uploads/` 对最大 1 MiB 的 gzip tar 验压缩校验、路径/类型/条目数、文件尺寸与重复路径，不解压到运行目录；本机 `DiskStore` 以 `(teamId,projectId,sha256)` 存受限字节，读回校验摘要。`POST .../sources/uploads` 要求 Owner/Developer 或单项目 `sources:upload` Agent；与 PostgreSQL 的 `source_uploads`/`upload_requests` 绑定，幂等键相同复用原 ID、不同字节 409；只返回 `stored_unverified`。GET 元数据必须再确认底层字节可读。源包上限与本地存储仅用于受控开发，多租户在线需要对象存储、累计硬配额与 GC。

## GitHub 接入

`internal/source/github/` 用 GitHub 官方示例 HMAC-SHA256 校验原始 webhook 字节；受保护的 RSA GitHub App 身份签发短期 JWT，向 `/app/installations/{id}/access_tokens` **明确请求单 Repo ID 和只读权限**，再核 `/repositories/{id}` 元数据，token 不写入 DB/日志。只有运维引导身份能指派 GitHub Installation/Repository 到一个 Project；未配置 App 身份时记录 `pending`，用户不能用猜测的 ID 自认他人仓库。`/v1/hooks/github` 仅在验签、delivery 去重、已验证绑定与仓库重验都成立时，写入 `pending_commit_verification`；未取回该 SHA 的 Git 字节，也没有 Build/Deployment 对象。普通分支删除事件返回 `ignored_deleted`，尚不执行分支别名清理。

## 验证与边界

单元先红后绿：无归档校验器/存储时构建失败；添加 gzip CRC 负例时发现 tar EOF 不检查压缩流尾，补有界 drain 后通过；超过 128 个目录条目的归档原先被放行，加入所有条目计数限制后通过。HTTP 集成先因来源/绑定选项缺失构建失败，补齐后验证两 Team、Viewer 拒绝、Agent 单项目上传、错误归档/超限、重复上传、重启读回、项目仍 `not_deployed`；GitHub 在 `httptest` 模拟端校验 App JWT 签名、单仓库 token 请求、错误 Repo ID 拒绝及 HMAC/重复 delivery/篡改/跨绑定拒绝。**没有真实 GitHub App PEM/Installation 或公网 webhook 联调，不得将模拟服务响应说成真实仓库所有权验收。**

**真实验证结果：**隔离 PostgreSQL 16 的 `web_test_*` Schema 上执行 `TEST_DATABASE_URL=<独立测试实例> go test -race ./... -count=1`，`cmd/api`、`internal/httpapi`、`internal/source/github`、`internal/source/uploads` 均 **PASS**（`db/migrations` 无独立测试）；`go vet ./...` 退出 0；OpenAPI YAML 解析通过，含 **12 个路径**；Markdown **9** 份文件相对链接检查 **0** 处错误。测试后的 `web_test_*` Schema 数量 **0**。测试实例不是旧平台数据库。

后续必须由 GitHub 用户授权/回调证明 Team 所有者可访问目标仓库，按固定 SHA 拉取并核对实际归档字节，改用隔离 Builder 与对象存储后才能启用真正的 Git/Agent Preview。当前未开启公网入口，单机本地目录缺累计容量硬限制，不能把 `stored_unverified` 或 `pending_commit_verification` 冒充已验证代码或已部署网站。
