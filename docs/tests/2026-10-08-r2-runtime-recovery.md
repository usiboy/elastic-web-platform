# R2 合成动态实例：持久 systemd 启动链与受控重启演练

**日期：2026-10-08。验收范围：**用户允许的同 ECS、内部 CA/精确 Host、只含合成数据。**没有重启整台 ECS**，仅对新平台 R2 网关、relay、Next 容器做受控停启；旧站、默认 Docker daemon、k3s 及其它服务未停用。

## 启动链修复

- 发现原 `ewp-preview-api-20261006.service` 和 `ewp-preview-site-20261006.service` 是位于 `/run/systemd/transient/` 的临时 unit，宿主重启后不会自动恢复；新平台 PostgreSQL 容器的 Docker restart policy 也为 `no`。因此之前仅保证运行时故障回静态，没有保证整台机器重启后 API/Gateway 可被 systemd 拉起。
- API 保持原资源/秘密文件边界，转换为 `/etc/systemd/system/ewp-preview-api-20261006.service` 的持久 enabled unit，未改变 API 二进制；转换后 Owner B `/v1/me=200`。仅把**新平台 PostgreSQL 容器** `ewp-postgres-preview-20261006` 的策略改为 `unless-stopped`，容器 ID 未变；旧纤莉秀容器和默认 Docker daemon 的配置未改。
- `ewp-r2-next-runtime-20261008.service` 是持久 oneshot/RemainAfterExit unit：在 Builder `PrivateNetwork=yes`、同一 image/source SHA 与固定 `--internal` bridge 前提下，若合成容器仍运行则实际 `/api/health` 读回；若它已停，则只启动并核验原固定 Docker 容器和 `172.29.77.2:3027`，不创建新 Deployment，也不更改 Stage Alias。root 专用 `ewp-r2-relay-persistent-20261008.service` 按依赖顺序进入 Builder netns、绑定原 `0600` Unix Socket，并在启动时拒绝误占宿主 netns/陌生端点。
- 旧网站网关临时 unit 因 reaper 服务的历史 `After=` 引用，在停用后仍保持 transient 已加载，无法原位改成同名持久 unit。第一次试切换遇到校验路径错误后触发自动回滚；尝试同名转换也未生效，均已重新启动旧临时服务并读回 Stage 200。最终改用**新名称**持久 unit `ewp-r2-site-gateway-20261008.service`，要求 API 与新 relay 先启动。切换前停新网关和旧 transient relay，核 Socket 停止，再起持久 relay、持久 Gateway；Stage 实际 TLS API 200、旧 IP 401，旧 A/B 路由正常。随后更新两个 reaper 的依赖并移除本轮临时创建但从未启用的同名网关 unit 文件。API、Gateway、Builder、Next 恢复 unit 与持久 relay 均显示 `/etc/systemd/system` FragmentPath 且 enabled；旧网关和旧 relay transient unit 不再参与开机路径。

## 实际恢复演练与边界

仅停止 `ewp-r2-site-gateway-20261008.service` 与 `ewp-r2-relay-persistent-20261008.service`，然后停止合成 Next 容器但保留 Docker 对象和私有网络；再次 `systemctl restart` 合成 Next 恢复 unit，它重新启动**同一容器 ID**并核 `/api/health`。顺序拉起持久 relay 和网关后，真实内部 TLS Stage `/api/hello=200`、数据库 Stage 仍是 `dep_be15038c6bc73cd168e2b2ed967c6f11:6`（**没有假装新部署**），旧 IP HTTPS 401、旧 PostgreSQL/旧纤莉秀容器 ID 与宿主 `docker0` 不变。脚本 `rehearse_r2_runtime_restart.sh` SHA-256 `1161fb7391fc058ca42727c244ed1784b688d6aa00d9c32f6f1ab86fb9f52bda`；新旧 A/B Preview、testA HTTP 生产、R2 Owner 私有 Preview 和 Stage SSR/API/流式响应的 ECS opt-in `go test -race` 再次通过。

**此演练验证 systemd 依赖顺序和仅 R2 进程停启后的自动拉起，不等于 ECS 整机重启、磁盘故障、默认 Docker daemon 重启或持久数据库异机恢复已验收。** 机器重启时若容器、归档、私有网络或受限密钥不满足保护条件，Next unit 应 fail closed，Gateway 依据此前代码回退 Stage 已验证静态版本；不能把逻辑预算称为物理独占保障。原站仍运行，合成 SQLite 试点的 tmpfs 不具备宿主重启持久性。
