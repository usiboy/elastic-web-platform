# 原 ECS 两次整机重启：首次数据库启动失败、修复后自动恢复实测

**日期：2026-10-08。用户授权两次整机重启；两次均已执行。** 验证范围是同 ECS 合成 R2 站、既有 A/B 静态 Preview、旧 IP 受限纤莉秀探针及新平台控制库。没有正式 DNS、独立故障域或真实快麦会话迁移，故不把此验证称为共享 SaaS 上线。

## 事前证据和第一次重启

第一次重启前：boot ID `68b90bcd-a0e5-4c39-9eca-e30819c6aaa9`；独立平台 PostgreSQL `pg_dump -Fc` 受限备份 `pre-full-reboot-20261008.dump`，SHA-256 `a88bda16a58c638874f1d46283dc2c3c1264e936aaa407cb69addaaba66670a8`，经 `pg_restore --list` 校验；合成纤莉秀 SQLite 快照 SHA-256 `a74e3d7ac5808f1c604fd40ede1e50a1b7ad5271de598cc53a2188fc1d52a255`。API、网站网关、Builder、Next/relay、纤莉秀合成实例/relay 与旧探针均为 enabled；先正常停止**仅合成纤莉秀**容器，确认写入锁消失，Next Stage 和旧探针仍各返回 200，再由 `systemd-run` 安排 8 秒后的 ECS reboot。

第一次启动后 boot ID 变为 `6022a7ca-a44d-427e-80a6-50eced653574`。旧探针容器 `3034af04913a...` 和 loopback `/api/live=200` 均恢复；但新平台 PostgreSQL 原容器 `7085d10db80f...` 不断以退出码 1 重启，`docker0` 因它未入网而为 DOWN，控制 API/网站网关短时间无法就绪。日志显示根因是原容器**以 root 执行入口脚本同时 `--cap-drop ALL`**：数据库首次初始化后目录属于 PostgreSQL 用户 UID70，重启时 root 无 `CAP_FOWNER`/`CAP_DAC_OVERRIDE`，不能 chmod 或读取目录。SELinux 为 Disabled；以只读挂载验证 UID70 可正常读取原 PGDATA，并可管理 UID70 的运行时目录。没有重启默认 Docker 或修改旧站。

在保持 `/data/ewp-preview-20261006/state/postgres` 原数据目录和上述受限备份不动的前提下，将原容器完整保留为**已停止** `ewp-postgres-preview-20261006.pre-uid70-recovery-20261008`（原 ID `7085d10db80f...`，restart policy `no`）。同一镜像与同一原数据库目录改由 UID70/GID70 的替代容器以原服务名启动，保持 `--cap-drop ALL`、只绑定 `127.0.0.1:15434`、同等 CPU/内存/PID 限制，运行目录为 UID70 私有 tmpfs；未加载快麦会话或复制 PGDATA。新 ID `44ac111bf05f284011c569218d733aaa4234cef3897cca86eb3f511c0f14caf4`、策略 `unless-stopped`，`pg_isready`、受限 DB URL 查询迁移 020 和 Stage 代际读回通过。随后单独对新容器执行一次 `docker restart`，UID70 仍能正常再次启动。恢复脚本 `deploy/ecs/recover_pg_after_full_reboot.sh` 仅识别原固定容器/归档哈希及精确替代目标，不能用于重启已失效的旧 root 容器。

## 第二次授权复验与结果

修复后另存受限数据库备份 `post-full-reboot-uid70-20261008.dump`，SHA-256 `fa62f232e39fe720b83e7b1071b239d83577d5c5af85730f504cb39282ae68c9`，经 `pg_restore --list` 校验；原 root 容器确认 stopped/restart=no，新 UID70 容器 running/restart=unless-stopped。所有持久 unit 和阿里云远程服务 enabled、`systemctl --failed` 为零；再次正常停止合成纤莉秀写者并核锁已释放，Next Stage/旧探针均 200。用户明确批准第二次 reboot，boot ID `6022a7ca-a44d-427e-80a6-50eced653574` → `eaea6d42-710d-4220-8fe3-9421d9d2678c`。

**第二次整机启动实现了自动恢复：**UID70 PostgreSQL 保持同一新 ID `44ac111b...` 且 running，`docker0` UP；API active（本轮因数据库启动顺序自动重试 1 次）、网站网关 active（重试 0 次），`ExecStartPre` 日志实际记录 `r2_boot_next_runtime=ready`。Next Stage 仍为 `dep_be15038c6bc73cd168e2b2ed967c6f11`、代际 **6**，没有误回退或创建假部署；两个 `runsc` 进程、各自 root-only relay、两个定时回收器均 active。原 IP HTTPS 匿名 401，旧受限纤莉秀探针 `/api/live` 与 `/api/health` 均 200；旧探针容器 ID 不变。

跨公网 IP 的内部 CA TLS opt-in `go test -race` 七项全部通过：A/B Preview 与跨租户保护、已签静态候选、testA 原 HTTP 生产别名、Next Stage SSR/API/流式响应、原纤莉秀合成 SQLite 私有 Preview（匿名401、A Owner 200、业务工具401、快麦登录403）。合成 SQLite `PRAGMA integrity_check=ok`、商品行数仍为 0，先前受限快照 SHA 未变。第二次启动后再次生成独立 `pg_dump -Fc` 备份 `post-second-full-reboot-uid70-20261008.dump`，SHA-256 `5077c6fc41edb62403303e1f8bae2ffd53321632702d82044ed4d524b797e057`，`pg_restore --list` 和迁移 020 读回通过；此前 `post-full-reboot-uid70-20261008.dump` SHA-256 `fa62f232e39fe720b83e7b1071b239d83577d5c5af85730f504cb39282ae68c9` 是**第二次重启之前**的备份。此次 `systemctl --failed` 为零，旧站和 default Docker 未被卸载。

## 保留的操作边界

**后续资产收敛：**在再次确认原容器 ID `7085d10d...` 为 exited/restart=no、现役 UID70 ID `44ac111b...` 为 running、三份备份 SHA/`pg_restore --list`、同一 PGDATA 挂载和旧探针健康后，已**仅移除旧停止容器对象**；现役容器 ID、原 PGDATA、三份备份及旧探针继续保留。旧容器与现役库的双写风险因此不再存在；一次性恢复脚本依赖已移除的原对象，**不可再次执行**。第二次证明的是**计划维护窗口**下的整机重启：合成纤莉秀写者在两次重启前都被正常停止以释放 SQLite 锁。真实纤莉秀数据库/快麦凭据从未上传此 ECS 新平台。
