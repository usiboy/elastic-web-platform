# 原 ECS Builder 试点运行与网络恢复说明

**范围：**只操作 ECS `i-bp19lwbwc5ej8pk0917s` 上新建的 `/data/ewp-builder-20261007/` 与 `ewp-builder-dockerd-20261007.service`；旧 `docker.service`、旧站容器及新平台控制 API 要继续提供原服务。经授权，同机 `runsc` 试点已实测，证据见[验证记录](../tests/2026-10-07-original-ecs-builder-ab.md)。

## 正常状态与停止

- `systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork -p ActiveState -p UnitFileState` 应为 `yes` / `active` / `enabled`。Builder Docker **仅**通过 root 可访问的 Unix socket `/run/ewp-builder-20261007/docker.sock`；用 `DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock docker info` 查询 `cgroupfs` 与 `runsc`，不要把 CLI 指向旧 `/var/run/docker.sock` 来执行构建。服务增加了 `ExecStartPre`：在独立 netns 中对照宿主 netns inode，不同才启动 dockerd；以 `systemd-run` 分别验证 `PrivateNetwork=yes` 返回 0、`PrivateNetwork=no` 返回 1，再由 `upgrade_builder_namespace_guard.sh` 带旧容器/Owner/旧 IP 回归重启本服务通过。此启动前断言是防再次删除宿主 `docker0` 的实际防线，不应跳过。
- 试点仅用 `bash /data/ewp-builder-20261007/state/build_fixed_ab_on_control_ecs.sh testA|testB` 串行构建两个固定摘要；脚本有 `flock`，不接任意 Team/Repo 上传。源包在仅含公开测试字节的 `/data/ewp-builder-input-20261007/`，GitHub App PEM 不在 ECS。2026-10-07 后续将新输出卷限定为每项目 64 MiB `tmpfs`；**重启后必须由该脚本调用 `prepare_bounded_builder_output.sh` 重挂并验证**，输出本身不持久，历史旧卷不能再用于签名。实际产物经哈希校验后，原固定私有 Host 读回不受此改造影响；见[硬限实测](../tests/2026-10-07-bounded-builder-output.md)。
- 固定 A/B 构建的整个容器（包括离线 npm、Vite、归档）由 `run_builder_deadline.sh` 设置 100 秒宿主时限；超时后检查并强制删除**仅此 Builder 具名容器**，失败归档清零。4 秒 `sleep 60` 真 `runsc` 负例已验证退出 124、无残留。若提示 `builder_stale_container`/`builder_cleanup_failed`，先停止接新作业，检查仅匹配 `ewp-builder-deadline-<testA|testB>-20261007` 的 daemon 残留及旧站健康，再决定是否人工删除；不要重启旧 daemon 或删除其它容器。
- A/B 此后还用两枚经明确授权的短期单仓库 Token 做一次 App 取件，控制库签发作业并让两份真实 `runsc` 产物以试点 Ed25519 声明进入**另外两个** `d-*` 固定私有 Preview。两枚 Token 已向 GitHub 撤销，ECS 文件删除，Signer 私钥在实际 HTTPS 读回后删除。重跑旧作业、依赖旧 Token 或假设私钥仍在磁盘均不应成功；新来源/任务需要重新经过审查的身份与密钥流程。详见[签名试点记录](../tests/2026-10-07-same-ecs-git-signed-ab.md)。
- 若发生授权 API 慢查询、`docker0` 变化、旧容器停止或磁盘空间接近不足，先 `systemctl stop ewp-builder-dockerd-20261007.service` 并停止本轮构建。此命令只停 Builder daemon，不重启旧 Docker、Nginx 或数据库；正常情况下旧容器应继续运行。停机不删 Builder 卷、原 DB 或旧站文件。

## 默认桥异常时的人工核实和恢复

**切勿盲目复用旧 veth 名。** 曾经在第二 daemon 未设置 `PrivateNetwork=yes` 时，宿主 `docker0` 消失，PostgreSQL 原容器进程和 ID 仍在，但 `127.0.0.1:15434` Docker proxy 连到容器 `172.17.0.2:5432` 处于 `SYN-SENT`，授权请求超时。后来只在核实默认网络元数据与 veth 成对对应后恢复。当前 Builder unit 的独立 netns 可用 `readlink /proc/1/ns/net` 和 `readlink /proc/$(systemctl show ... -p MainPID --value)/ns/net` 比较；两者必须不同，Builder 自身只见 `lo`。

若异常重现，先停 Builder，检查默认 `docker network inspect bridge` 中 `Subnet=172.17.0.0/16`、`Gateway=172.17.0.1` 和旧 PostgreSQL 的实际 IP/PID；再用 `nsenter -t <PostgreSQL宿主PID> -n ip -o link show eth0` 与宿主 `ip -o link` 的相互 ifindex 对照，**只有确认是同一对 veth 且无现有 docker0/冲突路由**，才手工重建旧桥并挂入该 veth。例如 2026-10-07 现场对应宿主 `veth93d5352@if217`、容器 `eth0@if218`，当时恢复的指令为：

```bash
ip link add name docker0 type bridge
ip addr add 172.17.0.1/16 dev docker0
ip link set docker0 up
ip link set <已重新核实的宿主 veth> master docker0
```

随后实测原容器 ID/运行态、`ip route get <实际容器IP>`、B Owner `/v1/me=200`、A/B 私有 Preview、旧 IP HTTPS 匿名 401。不要因为 Docker 状态显示 `Up` 就认定旧数据库网络可用；不要用重启旧 Docker daemon 代替定位或恢复。Builder 默认隔离网络 unit 在 ECS 重启后仍应重新跑 `/data/ewp-builder-20261007/state/start_networkns_builder_guarded.sh` 核 `docker0`/Owner/旧站；当保护条件不满足，该脚本会停 Builder，不继续接构建任务。

## 配置备份与退出门槛

默认 Docker daemon 原文件备份 `state/daemon.json.pre-runsc` SHA-256 `2e891b029c6cdd9316146a11821194f6d3097f37b151158235315b716b24e9c7`；只注册 `runsc` 后默认 runtime 仍是 `runc`，该文件 SHA-256 `376f7eb5fe2173dbd912c75f0791f333995b342eccc106d6bdd62cb9a040f902`。Builder unit 未隔离网络的旧版本保存在 `state/ewp-builder-dockerd-20261007.service.pre-networkns`，**不可**再启动旧版本；只有当前 `PrivateNetwork=yes` 的 unit 允许运行。旧无界构建脚本、旧签名 CLI 和截止时间改造前脚本分别备份在 `state/build_fixed_ab_on_control_ecs.sh.pre-bounded`、`state/ewp-operator-builder-pilot-linux-amd64.pre-bounded`、`state/build_fixed_ab_on_control_ecs.sh.pre-deadline`，仅供回溯，**不得**用于后续作业。同机 Builder 共用 4 CPU/15 GiB 宿主和 `/data`；即使新输出卷有硬额，宿主其它存储/故障域、自动签名交接仍未准入，因此不开放陌生租户和完整动态纤莉秀应用。
