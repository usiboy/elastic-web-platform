# 固定 Git 来源的一次性构建作业合同

**日期：2026-10-06。结论：仅隔离数据库内的控制面作业合同；没有独立 Builder，不启动构建，也不部署迁移 018 到 ECS。** 本轮只读 `workbench list ecs` 仍仅在杭州见原控制 ECS，上海/北京无可见节点；Workbench 配置只有一个默认 Profile，先前构建 ECS 未恢复。Mac Docker 仍只声明 `runc`。现有 ECS 仍为 16 次迁移、原 API binary，真实 Git testA/testB 保持待隔离构建状态。

## 新增的约束

- `IssueBuildJob` 是内部控制面函数，只有固定 Git Push 已进入 `awaiting_isolated_build`、仓库绑定仍为 `verified`、已核验来源存在、Project 逻辑预留足以覆盖声明 CPU/内存/存储时，才签发有效 15 分钟的作业。它不在在线 Source Worker 或租户 HTTP API 中调用，也不代表有可用物理节点。
- 作业带随机 `job_...` ID、随机 256 位 nonce 和 `expectedVersion` CAS。迁移 `018_github_build_jobs.sql` 只保存 nonce 的 SHA-256、来源/Team/Project、固定镜像摘要、Builder ID、资源申请、发放/失效时间及状态；签名记录新增指向作业的外键，新记录强制约束，旧 017 数据采用 `NOT VALID` 留待迁移核查。重试过期或遗失的作业必须显式以当前版本重新签发；旧 ID/nonce 随即失效，同一版本并发重新签发只有一个成功。
- 独立 Builder 以后签名的 `BuildStatement` 必须包含控制面分配的 `jobId`/`jobNonce`。`AcceptSignedBuild` 先在 Team 与 Git Push 的事务锁下对照当前作业、来源摘要、commit、镜像/资源/Builder 声明与签名、真实归档字节；首次登记要求作业未过期且 Project 预留仍有效。成功时作业与签名来源记录、私有候选及审计一起提交。完成后的相同签名只读回同一候选，不能重新创建 Deployment。工作台的“预留”仍是数据库逻辑预算，不是实际 cgroup/PID/存储配额。

## 已运行的反例与边界

测试生成的短期签名密钥、合成 Vite/静态归档和独立 PostgreSQL Schema 验证：无预留不发作业；无作业即使签名正确也不能发布；假 nonce、替换镜像摘要、作业签发前伪造完成时间、过期作业、已被 CAS 重新签发的旧作业均不能登记制品；Owner 释放预留后，即使已有有效签名也不能发布；恢复预留且作业仍有效才能生成私有候选并标记作业 `completed`，已完成作业不能重新签发。两路并发用同一 `expectedVersion` 重新签发，只有一次成功；原有跨 Team/Project、来源失权、篡改产物及并发签名重放仍通过。`go test -race ./... -count=1` 在专用 PostgreSQL 下各包通过，`go vet ./...` 无诊断；追加伪造完成时间和已完成作业重签用例后，定向 `go test -race ./internal/pipeline -run 'TestSignedBuild|TestBuildJob|TestConcurrentBuildJob|TestConcurrentSignedBuild' -count=1` 通过。

**仍未满足：**当前内部函数的 Builder ID、镜像摘要和 `runsc` 均是控制面/签名方的声明，尚未绑定经审查的独立节点与真实镜像、内核隔离证据、一次性安全下发通道及节点容量 ACK；没有在线调用或私钥分发。不能仅凭此测试签名把原 testA/testB 操作员 fixture 提升为平台自动构建，也不能应用迁移 017/018 到现有 ECS 并开放新的生产入口。只有独立节点、实测硬限和来源可信签名通过后才能接通。详见[整体差距收敛计划](../plans/2026-10-06-r1-r2-gap-closure.md)。
