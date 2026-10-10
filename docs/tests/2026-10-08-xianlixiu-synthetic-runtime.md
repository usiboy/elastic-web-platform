# 原纤莉秀应用的合成数据 R2 私有 Preview：真实 ECS 读回与恢复

**日期：2026-10-08。范围：**用户明确只允许合成数据进入新平台。本次从原项目代码打包 Express 服务及已构建的浏览器 UI，未复制本机真实 `db/product-pallet.sqlite`、压缩数据库种子、`.runtime/sessions`、`.env`、快麦 CLI 配置或任何 accessToken。原 ECS 受限探针 `xianlixiu-reviewed-empty-poc` 与新平台 Next Stage 都保持运行。

## 输入与实际托管

- 本机打包脚本 `deploy/runtime/xianlixiu-synthetic/build.mjs` 只输出原服务端 bundle、原已构建的 HTML/JS/CSS 和运行时所需的公开 `unzipper` 依赖；归档中的 4408 个条目通过路径/无符号链接检查，未含 SQLite、压缩种子、证书密钥或会话目录。归档 SHA-256 `afaf12b90deb1c89fe9ee7cbeea6ba15462bebefa3e4df32aae5797088d1eb25`，服务入口 SHA-256 `2f4513e140da1460c7d880f9d4512490d810f8416dd96d07c56e09424cb68e37`。本机真实启动包测试验证 `/api/live` 与 SQLite `/api/health` 200、首页 HTML、业务工具匿名 401、同库第二进程拒绝、正常退出后重启及合成 SQLite 在线备份/非覆盖恢复。
- 原应用增加 `EWP_SYNTHETIC_ONLY=1` 显式模式：`/api/auth/login` 在调用快麦认证器和写入私有会话配置**之前**直接返回 `403 SYNTHETIC_LOGIN_DISABLED`；默认本地/原站模式不变。原项目 `npm test` **37 文件、262 项通过**，`npm run typecheck`、`npm run build` 通过。ECS 运行实例环境只含合成配置，不安装/使用快麦凭据。
- 原 ECS 的独立 Builder netns 既有 `--internal 172.29.77.0/24` 网络上，新增固定 `runsc` 容器 `ewp-r2-xianlixiu-synthetic-20261008`（`172.29.77.4:3067`、CPU 1/内存 1 GiB/PID≤64、无 capability/禁提权/只读根/UID 1000）。受核代码只读挂载；仅合成 SQLite 与会话目录写入 Builder 专属 `/data/ewp-builder-20261007/state/r2-xianlixiu-synthetic-data/`，root-only 父路径、实际目录 UID1000/`0700`。独立 root 专用 relay `/run/ewp-r2-20261007/xianlixiu.sock` 只代理 `.4:3067`，网站 Gateway 没有 Docker socket。第一次试启动因 tar 解包的代码子目录为 `0700`、沙箱 UID1000 无法遍历而被拒绝；只将**合成代码目录**改成可读 `0755`（上层 root-only `state/` 未开放），重新启动成功，未改数据目录权限。
- 操作员核实健康后在 A Team 项目登记固定合成 Deployment `dep_8bade33cfc46ac07d0614a162eefd9ca`，私有 Host `d-8bade33cfc46ac07d0614a162eefd9ca.preview.ecp.lingtong.com`。内部 CA TLS 下匿名访问首页/健康/工具均 401；B Team 不能签发 A 的会话码；A Owner 单次会话后 `/api/health=200`、原 React HTML SHA-256 `43a94bf2c6364836446087345904f6799aa559234eefac913cfeeea867e9c698`、JS SHA-256 `80352eabdb4521ad94476585c570a3e7c5f0c694901804bb616650d4c1b3cbe4`，未登录业务工具仍 401，提交**仅合成标记** `DEMO-ONLY` 返回 403 而不产生会话。A 原 HTTP 生产别名和 R2 Next Stage 代际 6 没有切到该应用。

## 数据与实例回归

- ECS 新合成库由原 `ProductPalletRepository` 建表、健康检查，大小约 76 KiB，商品记录为 0。运行中的原应用通过其新 `backup-product-pallet` 命令生成受限一致快照（77,824 字节，SHA-256 `a74e3d7ac5808f1c604fd40ede1e50a1b7ad5271de598cc53a2188fc1d52a255`），恢复到**另一个**合成数据库，`PRAGMA integrity_check=ok`、商品数仍 0。ECS 真实第二个 `runsc` 应用实例指向同一数据库时，原 `*.writer.lock` 拒绝第二写者，主实例仍健康。
- 合成应用与独立 relay 从 transient 切为 enabled 的 `/etc/systemd/system` 持久 unit。仅停这个 relay 和合成应用容器，`SIGTERM` 后锁文件消失；由 runtime unit 重启**同一个容器 ID**、重新绑定原合成库，relay 恢复，备份 SHA 未变，私有 Host 再次通过 TLS/Owner 会话测试。原 Next Stage/A/B 私有 Preview、testA 静态生产与旧 IP 401 的 ECS opt-in `go test -race` 共 7 项回归通过；独立 PostgreSQL 下 `go test -race ./... -count=1` 全包通过、`go vet ./...` 无诊断。默认 `docker0`、旧 PostgreSQL/受限探针容器 ID 未变化，短期 Git Token 和试点签名私钥仍不存在。

**阶段边界：**原应用源码和静态 UI 已经在新平台私有动态路径以合成 SQLite 真实读回，但用户没有授权把真实业务数据/快麦会话上传 ECS，故订单、库存等需真实快麦调用的业务流程仍不在此验证；不能称原站完整迁移。当前合成目录在 `/data` 可跨容器停启，本轮没有执行整机重启或真实数据恢复。正式项目自动导入、动态资源全局硬额和真实业务身份仍是后续工作。运行与停止范围见[合成实例运行说明](../operations/2026-10-08-xianlixiu-synthetic-runbook.md)。
