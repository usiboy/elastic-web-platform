# 独立 Builder 的签名制品交接：仅隔离库代码合同

**日期：2026-10-06。状态：未接生产 Worker、未部署迁移 017/018、未执行陌生租户构建。** 这段能力只在本机独立 PostgreSQL Schema 与测试生成的短期 Ed25519 密钥下运行。现有 ECS 新平台数据库仍为 16 次迁移；现有真实 testA/testB Git 来源仍是 `awaiting_isolated_build`，不得把合成签名用例称为真 `runsc` 构建或准入成功。后续新增的一次性作业约束见[作业租约记录](2026-10-06-build-job-lease.md)。

## 交接合同

`internal/build/attestation.go` 定义有版本和域分离的 Ed25519 签名声明，绑定 Builder ID、Team/Project、Git Delivery ID、控制面一次性 Job ID/nonce、固定 commit、来源归档 SHA-256、产物归档 SHA-256、镜像摘要、运行时及声明的 CPU/内存/存储值。校验签名公钥、受限字段与**实际产物字节**；`runtime=runsc` 是签名方的声明，不是内核隔离证明。

`Processor.AcceptSignedBuild` 仅是内部函数，未接 HTTP、现有 `source-worker` 或操作者 CLI。它在事务中锁定 Team 与 Git Push，检查仍有效且属于同一个 Repo 的 `verified` 绑定、已核验来源对象的实际字节、来源 commit/摘要、受支持 Vite preset，以及后续新增的[当前一次性作业](2026-10-06-build-job-lease.md)与 Project 预留；再验证构建输出安全路径和文件限制，按项目保存不可变 Bundle、私有固定 Preview Host、审计与签名指纹，最后把状态从 `awaiting_isolated_build` 改为 `preview_candidate`。迁移 `017_signed_github_builds.sql` 为声明指纹、签名、来源和 Deployment 建立项目范围的外键与唯一约束；重复相同声明读回同一制品，不同声明不能替换已有来源产物。`preview_candidate` 只表示私有候选已登记，仍需真实 HTTPS 读回才能标记 `preview_ready`。

## 已运行的正反例

本机隔离 PostgreSQL 下的测试使用**测试生成**的签名密钥与合成 Vite Vue/静态 Bundle，验证正确签名产生且仅产生一条私有候选；签名公钥不匹配、Team/Project/commit/来源摘要变化、产物字节与声明不符、无效 tar.gz、Git 绑定撤销均无法生成部署。两次并发重放同一签名最多只有一个 Deployment、两个调用读回同一个 ID；签名声明变化无法覆盖旧候选，入库后真实 Bundle 字节可按摘要读回。该结果说明控制面交接约束，而**不说明测试代码真的在 `runsc` 里执行**。独立 PostgreSQL 下 `go test -race ./... -count=1` 所有包通过，`go vet ./...` 无诊断；追加跨 Project 反例后重跑 `go test -race ./internal/pipeline -run 'TestSignedBuild|TestConcurrentSignedBuildReplay' -count=1` 通过。ECS 旧 IP 与 A/B 站点没有应用迁移 017。

## 下一步准入边界

必须先取得独立 Builder 节点，核验 Docker/runtime 的真实 `runsc` 二进制与 daemon 归属、固定镜像和依赖缓存、cgroup/PID/磁盘/超时及恶意负载邻居隔离；再定义由控制面签发的一次性作业租约，把 Ed25519 **私钥只放在受控独立 Builder**、公钥只由控制服务的受保护配置加载。完成来源任务→沙箱构建→签名归档→服务端读回→私有 Preview 真字节的端到端试验，才允许将此内部函数接入任务流。没有这些外部证明时，即使操作员持有自制私钥能签署 `runtime=runsc`，也**不得**打开线上导入入口或自动把状态改成 `preview_candidate`；控制 ECS 不运行租户构建代码。前置探针结果见[构建隔离准入](2026-10-06-builder-preflight-gate.md)。
