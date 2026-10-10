# R1 自动交付增量：隔离构建作业超期持久失败与旧版保留

**日期：2026-10-07。范围：**在独立本机 PostgreSQL 中验证控制面作业状态，不签发新的 ECS 签名作业，不改动原 ECS 数据库/旧站。用户确认暂时只用原 ECS Builder，暂不提供公网域名；此增量不改变 R1 的 `blocked` 判定。

## 改动和验收

- 新增迁移 `019_expired_github_build_jobs.sql`，允许 `github_build_jobs` 从 `issued` 进入持久 `expired` 终态；已完成作业保持 `completed`，此前 018 的校验和不变。
- `Processor.ExpireOneBuildJob` 由常驻 Source Worker 在空闲时执行，按 Team→Push→Job 顺序加锁并复核过期状态。只有仍在等待隔离构建的 Push 才变为 `failed`、`last_error_code=build_job_expired`；已完成/尚有效的作业不受影响，失败不写 Deployment 或切生产别名。旧签名与旧 nonce 无法在终态后再接受，重复扫描和两个并发扫描者不重复处理。
- OpenAPI 版本升为 `0.11.0`，明确列表/详情的 `errorCode=build_job_expired`；现有工作台从真实 Push 状态读回，不用永远展示“待构建”替代终态。

真实 PostgreSQL/Race 定向测试首先在方法缺失时编译失败，实施后覆盖：未到期作业不误报、两个扫描者中只有一个提交过期事件、Push 错误码持久化、旧生产 Host 的 Deployment ID/代际保持、无新增 Deployment、晚到签名拒绝、同一失败来源不能暗中重签。运行带独立 PostgreSQL 的 `go test -race ./... -count=1` 全包通过，`go vet ./...` 无诊断。

**尚未满足：**这是控制面超期失败及用户可见错误码，常驻 Source Worker 在 ECS 不持有可续期 GitHub App 身份，尚未部署新版迁移/循环到线上，也没有已获独立故障域准入的自动 Builder、持久可撤权的仓库选择体验或公网受信 HTTPS。原 ECS 与控制数据库同宿主，无法借此证明陌生租户构建不会影响旧站。R1 的独立故障域和正式 HTTPS 门槛仍不通过。
