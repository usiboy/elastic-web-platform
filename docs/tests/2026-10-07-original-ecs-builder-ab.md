# 原 ECS 同机网络隔离 Builder：testA/testB 真构建、事故恢复和准入边界

**日期：2026-10-07。授权范围：用户允许原控制 ECS `i-bp19lwbwc5ej8pk0917s` 做 Builder ECS。此段结论：已在该机的新平台专用第二 Docker daemon 中、真正 gVisor `runsc` 下串行构建两个授权固定 Git 测试仓库；CI 静态文件逐字节一致，并由操作员导入 A/B 新私有 Preview。后续以短期单仓库 Token、一次性作业和试点签名接通了真实 Git 私有候选，详见[后续验收](2026-10-07-same-ecs-git-signed-ab.md)；仍无输出盘硬限、独立故障域或持续通用自动 Worker，不宣称 R1 SaaS 上线。**

## 主机、运行时与旧站边界

原 ECS `114.55.237.62` 4 CPU、约 15 GiB 内存，实施前可用约 13.7 GiB，`/data` 可用约 35 GiB；原 `xianlixiu-reviewed-empty-poc` 容器和新平台专用 PostgreSQL 分别约 117 MiB/42 MiB 常驻。默认 Docker daemon 的原配置 SHA-256 `2e891b029c6cdd9316146a11821194f6d3097f37b151158235315b716b24e9c7` 先备份于 `/data/ewp-builder-20261007/state/daemon.json.pre-runsc`。宿主已经安装的 `runsc`/sentry/shim 与官方 gVisor x86_64 `20260928.0` 发布包逐文件摘要相同（经官方 SHA-512 `4ce35ca83aef7f96b06cde668e0b23aa98b05aa1829508e974196c2a1e02786c95f5bf79315fd7ddcfd88fe7a00f083ed8053e25eff7673d28d5256440caae8b` 校验）；只需注册到 Docker，未替换旧站镜像/程序。原 daemon 默认 runtime 仍为 `runc`。

**已发生并恢复的回归：**原 Docker daemon 使用 systemd cgroup 驱动、宿主 systemd `239`；直接用该 daemon 运行 gVisor 报错“systemd version 239 not supported, please upgrade to at least 244”。`runsc-fs` 别名与给现有 runtime 加 `--systemd-cgroup=false` 参数未解除，配置均回滚。第一次试运行独立 data-root/cgroupfs daemon 但**未隔离宿主网络命名空间**，它删除了宿主默认桥 `docker0`，导致旧 PostgreSQL 容器 IP `172.17.0.2` 与 Docker proxy 间连接 `SYN-SENT`，新平台带令牌 API 暂时超时；旧 IP HTTPS 始终 401。停止试点 daemon 后，核对 PostgreSQL 容器 `eth0@if218` 与宿主 `veth93d5352@if217` 为一对，恢复 `docker0` 地址 `172.17.0.1/16` 并将该 veth 重新挂入桥。之后 A/B 授权 API、私有 Preview、testA HTTP 站点与旧 IP HTTPS 的真实回归全部通过，原容器 ID `7085d10db80f` / `3034af04913a` 未变。此项失败不能省略或记作“一直无回归”；恢复操作须按**当时**的容器 PID/veth 重新确认，详见[运维恢复说明](../operations/2026-10-07-same-ecs-builder-runbook.md)。

经用户再次选择同机网络隔离试点后，将**仅新 Builder service** `ewp-builder-dockerd-20261007.service` 设为 `PrivateNetwork=yes`；builder daemon 采用独立 Unix socket `/run/ewp-builder-20261007/docker.sock`（RuntimeDirectory `0700`）、独立数据根 `/data/ewp-builder-20261007/docker`、独立 containerd namespace，`--bridge none`、`iptables=false`、cgroupfs、默认 `runsc`。主机和 Builder netns inode 不同（实测 `4026531994` / `4026532737`），Builder 内仅 `lo`；启动守卫确认宿主 `docker0` ifindex、旧容器 ID、B Owner `/v1/me=200` 和旧 IP `401` 不变。Node 22.22.0 `runsc --network none` 烟测成功，Docker inspect 回读 `runtime=runsc`、`network=none`、`memory=268435456`、`pids=64`、`nano_cpu=1000000000`、只读根。此服务在实测回归后启用开机自启，但**没有自动领取未知租户的构建任务或开放 TCP Docker 端口**。

## 固定 A/B 构建与逻辑预算

GitHub App PEM 仍只在 Mac。只将已在 Mac 重新由 App 固定 Git blob 核验的公开源码包移至 Builder 只读输入区 `/data/ewp-builder-input-20261007/`（A SHA-256 `e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d`、B SHA-256 `4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3`）。固定 `node:22.22.0-bookworm-slim` 镜像 config SHA-256 `e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3`、Repo digest `dd9d21971ec4395903fa6143c2b9267d048ae01ca6d3ea96f16cb30df6187d94`，从旧 daemon 本机复制进 Builder 独立镜像存储。准备依赖时仅对两个公开固定源码在 Builder 内可信 runc 容器使用 host 网络运行 `npm ci --ignore-scripts`，A/B 分别放入独立缓存；实际构建改由 `build_fixed_ab_on_control_ecs.sh testA|testB` 以 `flock` 串行执行 `runsc`、离线 npm 锁文件安装、直接调用 Vite，**无网络**、只读根、无 capability、禁提权、UID/GID 1000、PID ≤ 64。A 上限 1 CPU/2 GiB，B 上限 2 CPU/3 GiB，工作目录分别为 1/2 GiB tmpfs，容器 60 秒超时。脚本只接受两份固定源码摘要与镜像 digest，拒绝未知名称。Docker 输出命名卷目前**没有总写入硬额**，因此即使只读根和 tmpfs 工作目录已限制，也不得开放陌生租户构建。

| 固定来源 | ECS `runsc` 构建输出 tar.gz SHA-256 | 与两家 CI 均相同的 HTML / CSS / JS SHA-256 |
|---|---|---|
| testA `e6062c7469d13712515dbf19474c851c044b753f` | `fcf4f4f5b3f08c46907b0d6269ac9044c94114b80502703e44d7deab6e1d563f` | `e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6` / `e1e8f6dd498f7017c7033bf78947a1fa907ede06cedbe10de3de842cf0be1531` / `78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a` |
| testB `cfaa2f283f5ced67b85899919fe10a4a9f99d014` | `a77d2355de350ee73ba99f555bf74b8ff19314a10802d03281b06ac49a557d2f` | `b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a` / `cee9771c708e34a72a0e3dde37cb41e83b0641928604a8f0151d59fbfc6f4e3c` / `0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b` |

此前 B 逻辑预算 1000m/2048/1024 MiB、预留 500m/512/256 MiB，无法覆盖此次 2 CPU/3 GiB 构建。独立新平台库写入前备份 `pre-testb-budget-builder.dump`（SHA-256 `170b1b1d1114ad7c1aece617d0814057f5cf89236e6f6ba6767e79051f2a8882`），用真实 Bootstrap/Owner B 作用域及 CAS v1→v2 改为 Team **2500m/4096/4096 MiB**、B Project **2000m/3072/2048 MiB**，API 读回 `reserved_not_provisioned`。A 的 1000m/2048/2048 MiB 项目预留未变。两项目逻辑预留合计 3000m/5120/4096 MiB、构建严格串行，但这仍不是平台的全局物理容量 ACK。试点的 96 MiB 内存负例退出 `137`，未知源码名在容器启动前被拒绝，原容器和旧 IP 未回归。

## 私有 Preview 与仍未打通的自动发布

导入前对**新平台独立**库做 `pg_dump -Fc` 并以 `pg_restore --list` 校验，`pre-ecs-runsc-fixtures.dump` SHA-256 `93045747cc3e03794a6ce782ff0775073eed4557c2e8b0f44a9200795e196fe6`。实际 ECS `runsc` 输出先核真实归档 SHA 后以**操作员可信 fixture**方式登记，未改旧 Preview Host，也没有晋升生产：

| Project | 新私有 Host | Deployment / Bundle 摘要 |
|---|---|---|
| A | `a-ecsrunsc.preview.ecp.lingtong.com` | `dep_1da7112930227a3cd70fc1766185af1c` / `209860fb26bcc3f9e8ae88f3c11d6c25911c8dc83b8bae75e62241247f092b65` |
| B | `b-ecsrunsc.preview.ecp.lingtong.com` | `dep_4616d311154b549d18c7b44d8bdc05bc` / `673adfcf729f7baaa54595e28a36cf078dc47b5594c2b4952b1eac8ac2e82f9d` |

真实 ECS opt-in Go TLS 验证新 A/B Host 匿名 401、跨租户不能签发会话、受保护 HTML/JS 实际正文逐字节等于 CI、A Cookie 不能访问 B；旧 A/B Preview、新 B `cfaa2f2` fixture、testA 原 HTTP 生产 SHA、旧 IP HTTPS 401 回归均通过。**线上仍只有操作员 fixture 导入**，不是 Source Worker 从 Git 自动取件→控制作业→节点私钥签名→网关读回；Mac 上测试临时签名只用于隔离 Schema，迁移 017/018 未上 ECS（保持 016），GitHub App 私钥未上传。继续上线前仍需总输出盘额/用量、可信 Git 令牌或源证明交接、Builder 专用私钥受控签发/撤销、真正自动状态流转、两租户并发与敌意构建隔离、正式 DNS/HTTPS，并承认同机旧站共用宿主故障域。
