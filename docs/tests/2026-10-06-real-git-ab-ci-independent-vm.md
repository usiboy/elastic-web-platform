# testA/testB 真实固定 Git、CI、独立 VM 构建与私有 Preview 复验

**日期：2026-10-06。结论：指定的两份公开测试仓库现在都可从真实固定 Git 源码构建、CI 产物逐字节复核；仍不是新平台自动强隔离 Builder。** Mac 新建专用 `ewp-builder-20261006` Colima ARM64 VM（3 CPU/6 GiB/28 GiB、关闭 Kubernetes、禁 SSH agent 转发、只读挂载本轮公开源码目录），与旧 ECS、默认 Docker VM、原纤莉秀项目隔离。该 VM 的 Docker 当前只有 `runc`，`go run ./cmd/builder-probe` 在指向此 VM 时按预期返回 `isolation_unavailable`；不能把这份实验视为陌生租户构建准入。

## 真实源码与 testB 修复

| 仓库 | GitHub main 固定提交 | GitHub App 重新逐仓取回的源码包 SHA-256 | 结果 |
|---|---|---|---|
| `usiboy/testA` | `e6062c7469d13712515dbf19474c851c044b753f` | `e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d` | 纤莉秀合成数据 Vue 模板，无真实快麦会话。 |
| `usiboy/testB` | `cfaa2f283f5ced67b85899919fe10a4a9f99d014` | `4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3` | 原 `9ea1fc0` 默认配置在独立 VM 仍于 Rollup `transforming` 超过 90 秒；testB **专属** `treeshake:false` 后约 0.35 秒完成。没有把该配置设为其他 React 项目的平台默认值。 |

用户明确授权后，testB 正常提交/快进推送 `cfaa2f2`，同时新增 `.github/workflows/verify.yml`。GitHub Actions [run `37475046275`](https://github.com/usiboy/testB/actions/runs/37475046275) 对固定 SHA 的 `npm ci --ignore-scripts`、`npm run build` 和 7 天静态制品上传均成功；没有跳过检查或改写旧历史。测试仓库修复详情写在 testB 自身中文 README 中。

## 两仓库独立 VM 构建和逐字节复核

Mac 私有 GitHub App PEM 仅用于**本机只读**获取源码；未进入 VM、ECS、容器或仓库。专用 VM 只挂载前述源码包为只读文件。固定 Node 22.22.0 Alpine 镜像 config SHA-256 `e4bf2a82ad0a4037d28035ae71529873c069b13eb0455466ae0bc13363826e34`；先对 A/B 分别在无宿主秘密的容器中运行 `npm ci --ignore-scripts` 填充**独立** npm 缓存，然后生产构建阶段按项目离线 `npm ci --offline --ignore-scripts`，直接调用 Vite，使用 `--network none`、只读根文件系统、无 capability、禁提权、UID/GID 501、PID ≤ 64、CPU ≤ 2、内存 ≤ 3 GiB、临时工作目录 tmpfs 2 GiB。上述约束是本地 `runc` 实验，不是 gVisor 或总磁盘硬配额实证。

两仓库各重复构建至少两次，产出的**HTML/CSS/JS 内容字节**与各自 GitHub Actions 成功制品一致；tar.gz 打包时间不用于跨环境等价判定：

| 站点 | HTML SHA-256 | CSS SHA-256 | JS SHA-256 / 大小 |
|---|---|---|---|
| testA | `e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6` | `e1e8f6dd498f7017c7033bf78947a1fa907ede06cedbe10de3de842cf0be1531` | `78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a` / 74.25 KiB |
| testB | `b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a` | `cee9771c708e34a72a0e3dde37cb41e83b0641928604a8f0151d59fbfc6f4e3c` | `0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b` / 188.58 KiB |

testB 成功制品在浏览器桌面与 390px 移动端实测：`21→63`、`123→369`、`10001` 显示范围错误，整页无横向溢出或 JS 错误。独立 VM 的 96 MiB 内存负例退出码 `137`，没有留下构建容器；它仅验证这一测试命令被 cgroup 拒绝，不能替代恶意租户邻居隔离、CPU/PID/磁盘全套安全门槛。

## 新平台 B 私有 Preview 与仍未放行的门槛

操作员先对**新平台独立** PostgreSQL 做 `pg_dump -Fc` 备份 `pre-testb-cfaa-fixture.dump`，用 `pg_restore --list` 验证，SHA-256 `6fc012a6f4d951461366659045343099e5d9209f66c1c62c2bbe2f7a8de8ffdf`。把真实 GitHub App 来源包和已与独立 VM 逐字节一致的 CI 静态制品打包成 `testB-ci-cfaa2f2.tar.gz`（SHA-256 `84185759f85cb3e90756642f0f1d63efef87cf2db3168306588bbb7774cac78b`），仅以现有**操作员可信 fixture** 路径导入 B 测试 Project，得到 `dep_59237010d4c786bc7786397aa9ce8015` 和新固定 Host `b-cfaa2f2.preview.ecp.lingtong.com`，Bundle 摘要 `d0fb12f2655622fd78b1ccb4b0e3e75837eeef490ea35f7ca3f960bee1a6959c`。没有替换旧 `b.preview` Host，也没有晋升 B 的生产域。

真实 ECS opt-in TLS 测试验证：匿名新 B Preview 401、A Owner 不能签发 B Preview 会话、B Owner 受保护会话读到上述 CI HTML/JS 摘要、新 B Cookie 不能用于旧 B Host；旧 A/B Preview、testA 公共 HTTP 模板与旧 IP HTTPS 匿名 401 回归通过。操作员声明不是 Builder 节点的签名证明；不能将这个测试租户的手工导入称为平台自动从 Git 生成 Vite Deployment。

**此段完成时尚缺：**可用并经核验的独立生产 Builder、`runsc` 实际安装与启动、离线依赖源审查、总磁盘额度/用量、控制作业与 Builder 私钥安全通道、签名产物的真实节点 ACK、自动 Git→Preview/生产切流、正式 DNS/HTTPS。后续已在**Mac 独立实验 VM** 通过官方 SHA-512 并完成 `runsc` 两仓库真实构建，详见[后段记录](2026-10-06-runsc-real-ab-handoff.md)；不能以实验 VM 冒充常驻云端 Builder。迁移 017/018 仍只在本机隔离 PostgreSQL 测试库；ECS 保持迁移 016 和原控制 API。
