# 同机 Builder 固定 A/B 输出卷硬限实测

**日期：2026-10-07。范围：**仅授权的 `usiboy/testA`、`usiboy/testB` 固定公开来源；在原 ECS 的独立网络命名空间 Builder 中验证输出写入上限，不签发新作业、不变更既有 Deployment/生产别名。此前两份已签名候选的构建发生在此次改造之前，不能倒称它们也受此硬限保护。

## 实施与可复现证据

先在新建的独立探测卷挂载 `size=8m,nosuid,nodev,noexec,uid=1000,gid=1000` 的宿主 `tmpfs`，从真实 `runsc` 容器向 `/out` 写入 16 MiB。`probe_builder_output_quota.sh` 检查容器可见容量不超过 8 MiB、`dd` 因 `ENOSPC` 失败、文件长度不超过 8 MiB；脚本退出后卸载并删除探测卷。该反例通过，证明限制来自实际内核挂载，而不是应用侧长度校验。

保留原无界 A/B 命名卷及历史制品不动，新增 `ewp-builder-{a,b}-output-bounded-20261007` 两卷。`prepare_bounded_builder_output.sh` 只接受固定 A/B 名称，核对 Builder `PrivateNetwork=yes`、daemon 活跃、卷的本地驱动及**准确挂载路径**；拒绝在已有底层文件上遮盖，先挂载 64 MiB `tmpfs`，再核对类型、容量、`rw,nosuid,nodev,noexec` 与 UID/GID 1000。后续调用必须重复核对，挂载失效时不使用普通磁盘卷继续构建。真正构建是 `runsc`、离线 npm、只读根、只读依赖缓存和禁网络；A `/work=960 MiB`、B `/work=1920 MiB`，`/tmp=64 MiB`、`/out=64 MiB`。B **这三个主要写入路径**的上限相加为 2048 MiB，但 Docker 的 `/dev/shm` 等其它写入面及 daemon 自身空间并未纳入该和数，不能据此断言作业声明的 `StorageMiB=2048` 已获物理硬额 ACK；禁用容器日志持久落盘，避免构建输出绕过卷额写到 Docker 日志。

线上 Builder 重构固定 A/B，产物 HTML/CSS/JS 各自 SHA-256 与前述 GitHub Actions 制品逐字节一致；两个新输出卷的 `findmnt` 均为 `tmpfs 64M rw,nosuid,nodev,noexec,...,size=65536k,mode=700,uid=1000,gid=1000`。新 `ewp-operator-builder-pilot-linux-amd64` 仅指向新卷，`verifiedOutput` 在读档前独立检查宿主 `/proc/self/mountinfo` 的精确挂载路径、类型、大小和权限；单测覆盖无挂载、超额、回落 ext4、缺 `noexec` 和错误属主。旧程序/构建脚本已在 Builder 的 `state/` 下备份，后续增加全容器截止时间后构建脚本最终 SHA-256 `53762d655e0ad911ab29cf41ed5267278b7452f27b32e63930968168a024e289`、准备脚本 SHA-256 `e90b0deb7230dd8d2f1b1f63f459933c90016fe7398deb324de76a2793184898`、试点操作员程序 SHA-256 `1c28a5bddd8c7e7fe4d7091e9005f1b4423d6c8b42acefd1a78f6f42525e734d`。

再跑原 ECS 的 A/B 私有 TLS Preview、A HTTP 静态生产、testB CI fixture、签名 `d-*` Host 和旧 IP HTTPS 401 的 4 组 opt-in `go test -race`，全部通过。原两个容器 ID 未变、`docker0` 保持 `172.17.0.1/16`、Builder service active；已撤销的短期 Token 文件及试点签名私钥仍不存在。`go test -race ./cmd/operator-builder-pilot -count=1`、`go vet ./cmd/operator-builder-pilot` 通过。

## 尚未通过的准入

挂载是**同宿主的内存型输出上限**，不等于跨租户持久存储配额或独立故障域；两卷产物在重启/卸载后消失，已交给平台的历史签名 Bundle/Preview 不依赖这两个卷。受信的缓存准备阶段仍用宿主 `/data`，Builder 的 Docker 自身存储、审计日志、物理容量 ACK 与全局回收/告警尚无端到端硬限；恶意依赖脚本、邻居压力、构建后自动签名交接未验收。此处只解除固定 A/B **输出卷无界写入**这一局部阻断，仍不能开放陌生租户或宣称 R1 上线。

## 后续：覆盖完整构建容器的截止时间

最初容器内 `timeout 60` 仅约束 Vite，不约束源码解包、`npm ci --offline --ignore-scripts` 或最后归档。现用 `run_builder_deadline.sh` 对固定 A/B 的整个 `docker run` 设置宿主 100 秒截止和 5 秒杀停宽限；使用固定具名容器、拒绝旧残留、在超时/失败后由 Builder daemon 强制清理，再核容器不存在。固定构建脚本失败时删除本轮 `/out/bundle.tar.gz`，禁止把可能写完但返回失败的产物拿去签新作业。容器内原 60 秒 Vite 时限继续保留。

真实 `runsc` 负例在同一镜像中执行 `sleep 60`，宿主 4 秒超时返回 124、实际耗时 4 秒，`docker ps -a` 无残留；脚本 SHA-256：包装器 `dc474072aeddc836e14a6ba8ada6192a55508556e3158ea56f9e878cd07b7f9a`，负例探针 `2d73316b2badb9f3d257feeb31359d4d5f3e85dfa955c09de67df83fd918391d`。上线重构时曾因漏写进入 `/work/site` 的 `cd` 使 A 的 `npm ci` 报缺锁文件并返回 1，容器按期清理、无新签名/生产切流；恢复该目录切换后，A/B 均由新包装器重跑成功并保持原 CI 哈希。再次运行 4 组 ECS opt-in `go test -race` 全通过，旧 PostgreSQL 与旧站容器 ID 不变、宿主 `docker0` 保持正常、试点签名私钥和短期 Token 文件均不存在。

此证据覆盖**固定构建容器执行时限与清理**，不等于任意前置依赖缓存填充、所有制品验算/调度步骤也在一个跨节点总作业期限内；daemon 无响应或宿主被杀停时仍须人工审查残留，不能把同机试点升级为通用恶意租户准入。
