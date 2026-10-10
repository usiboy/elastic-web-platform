# 原 ECS 合成 R2 功能闭环：查询、重建和回退

**范围：**仅 `r2-stage.preview.ecp.lingtong.com` 和两条 `d-*.preview.ecp.lingtong.com` 固定合成候选；原 HTTP A 生产别名、原 ECS 旧站与默认 Docker 不在此切流范围。未提供正式 DNS，读回使用内部 CA 和精确 Host/`--resolve`。

## 状态与故障

- **2026-10-08 后续现场：**新网站网关 `ewp-r2-site-gateway-20261008.service`、控制 API `ewp-preview-api-20261006.service`、Builder `ewp-builder-dockerd-20261007.service`、合成 Next 恢复 unit `ewp-r2-next-runtime-20261008.service`、持久 relay `ewp-r2-relay-persistent-20261008.service` 与 `ewp-r2-synthetic-reaper.timer` 应为 active/enabled，前五者的 FragmentPath 应在 `/etc/systemd/system`；旧临时 `ewp-preview-site-20261006.service`、`ewp-r2-runtime-relay-20261007.service` 不再是入口。Stage 当前代际 6 仍指向健康 v2 `dep_be15038c6bc73cd168e2b2ed967c6f11`，v1/v3 失败记录留作审计；v3 reaper timer active，v3 容器/relay 已回收。网站网关二进制 SHA-256 `0949a16be3059e5ee8ed17cb9c9fdaa627807dbe5b68a16d763ac22955cc1829`、迁移仍为 020；变更前备份 `state/pre-r2-multiversion-20261008.dump` 与 `state/ewp-site-gateway-linux-amd64.pre-multiversion`。`/run/ewp-r2-20261007/runtime.sock` 是 root 专用，不放到容器内。
- 网关每 2 秒重载快照并核实际 `/api/health`；Stage 运行时卡死后对比之前代际 CAS 回到已验证静态 Bundle。`ewp-r2-synthetic-reaper.timer` 每次只核**最新**试点 Deployment 的状态，发现 failed 即删除固定名称的测试容器/隔离网络，停止 relay；不能用它清理其它 Docker 对象。网关与 relay 不持有 GitHub App 私钥/快麦会话。
- `rehearse_r2_runtime_restart.sh` 可仅对合成 Node、relay、新网关做停启演练；它在当前固定 Stage `dep_be15038c6bc73cd168e2b2ed967c6f11:6` 前置核对后工作，不适用于后续别名改变时直接重跑。已实测原容器 ID 不变、Stage 仍为代际 6、旧站和 A/B 回归。宿主重启**未实测**；若恢复脚本无法重用 Docker 对象/私有网络或健康探针失败，应让网站网关保守回到原静态版本，不盲目删除旧站和原数据库。
- **2026-10-08 两次整机重启的后续事实：**[实测记录](../tests/2026-10-08-r2-ecs-full-reboot.md)替代上一条“宿主重启未实测”的历史状态。首轮原 PostgreSQL root+全 capability 移除导致 DB 启动失败，已保留其停止容器，使用 UID70 新容器绑定**同一** PGDATA 恢复。第二轮计划重启后 PostgreSQL、新 API/网关、Next/纤莉秀合成实例与 relay 自动恢复，Stage 原 Deployment/代际 6 不变。控制/站点与旧探针回归通过；异常断电与真实业务数据不在这轮范围。
- 当前新平台数据库容器仍名 `ewp-postgres-preview-20261006`，UID/GID `70:70`、ID `44ac111bf05f284011c569218d733aaa4234cef3897cca86eb3f511c0f14caf4`、`unless-stopped`。原 root 容器 `7085d10db80ff59ed3e8bacc735ac39a8b730d5f7cbf6d9723df8df633237735` 在保留停机对象完成两次重启验收后，已核现役库及三份备份并**仅删除旧容器对象**，不删除 PGDATA。本机备份为 `state/pre-full-reboot-20261008.dump`、`state/post-full-reboot-uid70-20261008.dump` 和 `state/post-second-full-reboot-uid70-20261008.dump`，均已过 `pg_restore --list`。旧对象已不存在，不得重跑依赖它的 `recover_pg_after_full_reboot.sh`。
- 若经核验的 relay/容器在**运行中**意外失联，网关回退 Stage 上一健康动态或静态版本并关闭失效固定 Preview；`site_runtime_pilots.state=failed` 后旧 Deployment 不能重新晋升。宿主重启时 API、Gateway、合成 Next 恢复 unit、持久 relay 和新平台 PostgreSQL 的启动配置已到位，但整机重启仍未实测；若恢复前网关先观察到不健康，也只允许保守回退，不能拿健康脚本直接改失败状态。

## 人工退出与重建

在试点回退时先用受限 `ewp-r2-operator-runtime-loop-linux-amd64 --action rollback --expected <当前代际> --deployment dep_f243de7290f4ae2f40c0bcaa626185f5` 把 Stage 切回已验证静态制品（当前代际 6 则期待回到 7）；读回静态首页摘要 `e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6`。再停两个合成 reaper timer、确认 v3 容器已清理、运行 `/data/ewp-builder-20261007/state/stop_r2_synthetic_loop.sh` 停 v2，检查旧 IP 401、A/B 私有 Preview、旧容器 ID、`docker0`；不要重启旧 Docker 或删除原项目数据。配置备份：`state/pre-migration20-r2-loop.dump`、`state/pre-r2-multiversion-20261008.dump` 和两段旧网关二进制，迁移及回退手册中的失败前提需要逐项核实。

**只停/启且数据库候选仍为 ready：**持久 unit 与 `start_r2_synthetic_loop.sh` 可按原固定摘要恢复**同一** `runsc` 容器/Deployment，再核私有 Preview 与 Stage，不能凭重启动作虚增代际。**已标记 failed：**脚本拒绝把失败记录冒充新版本；需核实受限归档/实例回收和当前代际，在受控新实例就绪后用操作员 CLI `--action register` 取得**新的** `dep_*` 和固定私有 Host，再经实际 HTTP/TLS 读回与 `--action promote --expected <代际> --deployment <新 ID>` 晋升。CLI 仅允许固定 A Team/Project、固定摘要和受限 Socket，不可用于真实业务应用或陌生租户代码。

双版本试验的 v3 是新独立 Socket/容器，使用 `start_r2_next15_v3.sh`、`--action register-v3`、`ewp-r2-v3-reaper.timer`；仅确认 v2 仍健康才可晋升 v3。新版本失败时网关先回退到仍健康的上一动态部署，v3 回收器**只**清理 v3，不停 v2/其 relay/共享桥；若 v2 随后也失联，网关继续从有限路由历史寻找已验证的更早静态版本，不能循环回已标记失败的 v3。当前 v3 已故障，重建仍需操作员重新校验固定归档并创建新 ID，不得把旧失败候选直接重新晋升。

用户已允许未来退役原 ECS 旧站，但本轮没有触碰它的数据或停机；只在替代的真实业务路径、备份/恢复与授权验收通过后才讨论停用。
