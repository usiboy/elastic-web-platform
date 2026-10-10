# 四项交付工作的实测进度与未完成边界

**日期：2026-10-08。范围：**用户要求直接开发 Git 自动发布、项目工作台、纤莉秀合成版本路径以及旧数据库容器收敛；不再要求突发断电验证。以下只列实际已写代码、已上 ECS 的内容及尚不能称完成的交付，不把此前固定试点当作通用自助服务。

## 1. Git→隔离构建→私有 Preview→旧版保留

- 新迁移 `021_failed_github_build_jobs.sql` 允许构建作业显式 `failed`。`pipeline.FailBuildJob` 要求精确 Team/Project/Delivery、签发的 ID 和随机 nonce，限制公开失败码为四种，锁序与签发/验收/过期一致；失败只改变作业和该 Git Push 状态，不创建 Deployment，也不切 Alias。固定试点 CLI `operator-builder-pilot -stage fail -failure-code build_failed -project testA` 已接入这一接口；不会读取签名私钥或把沙箱原始日志写入 UI。
- 独立 PostgreSQL 下 `TestFailedIsolatedBuildKeepsOldAliasAndRejectsLateSignedOutput` 通过：旧生产代际 7 不变、错误 nonce/任意错误文本拒绝、重复报告幂等、失败后的旧签名不能验收。超期并发失败测试也通过。
- **未完成：**当前 ECS 没有持久 GitHub App PEM（仅 Mac 保存）、此前两仓库临时 Token 已撤销，Builder 只对固定 testA/testB 来源/输出摘要运行离线构建，签名私钥按试点结束已删除。不能自动从下一次真实提交获得可信身份或自动构建新摘要；因此没有启用常驻 source-worker/自动 CD，也没有演示成功与失败**新提交**。上述 `-stage fail` 仅在新的固定受控作业已签发后才能使用；部署和回滚仍由既有试点/Owner 完成。仓库选择器及通用隔离执行协议仍是必要工作，不能把本段测试称作第一项完成。

## 2. 项目工作台

- 新控制面只读 `/v1/teams/{team}/projects/{project}/activity`：仅从数据库审计白名单中返回与该项目的真实 Push/Job/Deployment 相连的最近 30 条事件；不转发构建容器 stdout 或凭据。`/usage` 返回**全部**已核验 Git 源码归档字节、已核验来源数、已验收签名产物数；CPU、请求和持久存储缺计量源时为 `null`。Git Push 分页补真实 sourceBytes、job issued/completed/failed/expired 和签名验收时间。控制 API 已以迁移 021 上线，内部 CA TLS 外部 A/B Owner 读回：A 源码 23,526 B / 1 签名制品 / 10 事件，B 16,507 B / 1 制品 / 7 事件；跨 Team 两入口均 404。
- Vue3 工作台使用上述真实接口显示项目事件、交付状态与已核源数据；旧版按钮标识为“回滚至此版本”，生产生效仍受网关实际读回门槛控制。`npm run build`、6 项 Vitest 通过。
- **未完成：**可选仓库列表与公网 OAuth 回调仍不具备有效现场身份；工作台目前只在本机使用短期 Bearer，未部署公网浏览器入口；CPU/请求/持久存储实际用量、原始构建/运行日志无计量/可信采集器，不能显示虚构数字。A/B 完整“从界面选库→持续发布→查看实测用量”的故事尚未通过。

## 3. 纤莉秀合成版本

- 原项目新增 `version-handoff.test.ts`，用真实 SQLite 连接核验旧版本持写锁期间新版本同库无法启动、在线快照、停旧写者后新版本修改、快照不得覆盖现役库、旧值仅能恢复到**独立路径**。本机测试与原项目全量 37 文件/262 项基线通过；新增测试后应为 38 文件/263 项。ECS 原应用合成 Preview 和 SQLite 摘要继续保持原样；全平台 7 项 TLS/Race 回归通过。
- **未完成：**本轮没有把更新后的纤莉秀代码另建新运行容器、另签发 Deployment，也没有执行真实 ECS 合成版本切换。不能把 SQLite 文件级交接测试当作真实新包/新实例上线或业务数据回滚。

## 4. 旧数据库对象与运行资产

- 删除前现场核对旧 root PostgreSQL 容器 `7085d10d...` exited/restart=no、现役 UID70 `44ac111b...` running、三份逻辑备份摘要及 `pg_restore --list`、两者指向同一 PGDATA、旧探针 200。随后**只移除旧停止容器对象**，不删原 PGDATA、现役 PostgreSQL、旧探针或默认 Docker；删除后 UID70 仍 running、旧探针 200。一次性恢复脚本因旧对象不存在已不可重跑。
- 控制 API 上线前后还分别创建受限 `pg_dump -Fc` 并核 `pg_restore --list`：`pre-m21-api-upgrade-20261008.dump` SHA-256 `82b33c6ddaf0c607655e6e333ae6dc14f67c84cc95670d0a960e8614b7232fd3`；`post-m21-api-upgrade-20261008.dump` SHA-256 `216555e4fabe7edaa6d919c5b12bfeb8a8a71be720b929a3f2d0a46d00157e42`。控制 API 新二进制 SHA-256 `b17f3edb378c9ba187d372b3b0c6ea7e90e032d37373984d525ee6669fa62fe5`，旧可执行文件留在同一 root-only bin 下供回退。

## 验证方式及结论

本机临时独立 PostgreSQL 执行 `TEST_DATABASE_URL=... go test -race ./... -count=1` 全包通过（包括新迁移 021、失败保旧版与跨项目状态接口）；`go vet ./...` 无诊断；工作台 `npm run build && npm test` 通过。原项目 `npm run typecheck` 和 `npm run build` 通过；一次与 Go Race **并行**的全量测试出现单次 supertest `Parse Error`，隔离单测与不并行重跑 37/262 均通过，未改业务代码掩盖偶发现象。ECS 新 API/旧 Gateway/Stage/合成 Preview/A-B/旧 IP 的 7 项 opt-in `go test -race` 全通过。

**结论：**第 4 项现场收敛完成；第 1～3 项只有可验证增量，尚不能诚实宣称“项目全部完成”。真实 Git 身份续期与受控自动 Builder、工作台仓库选择/计量及纤莉秀新版本 ECS 发布，是后续交付的明确入口。仍只使用合成业务数据；用户已取消突发断电验证。
