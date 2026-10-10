# 真实 GitHub testA/testB 固定来源与独立 ECS 受控构建

**日期：2026-10-06。性质：只针对用户授权的两个公开测试仓库。** 新平台源码、测试库与旧产品分离；App 私钥只在当前 Mac 上读取，未上传 ECS、Git、容器或日志；短期 GitHub Token/JWT 未输出。旧控制 ECS 80/443、18092/18093 与原数据库均未变更。

## 真实 GitHub 来源

官方 App ID `5187675`、安装 ID `167883648`，针对每个 Repository ID **单独申请只读令牌**并取回固定完整 commit/tree/blob、对实际字节生成本项目的新 tar.gz；`TestLiveScopedRepositories` 正反代码合同在真实 GitHub HTTP 下通过。随后本项目 opt-in `TestLiveAppPollsTwoBoundRepositoriesWithoutBuildingVite` 在隔离 PostgreSQL 16 中，以真实 App API 主动查询两仓库主分支（无需公网 webhook），分别产生 `origin=app_poll` 的来源，Vite 均保持 `awaiting_isolated_build`，**Deployment=0**。测试数据库临时 Schema 随测试退出清理。

| 测试仓库 | Repository ID | main commit | 本项目实取归档 SHA-256 | 框架 |
|---|---:|---|---|---|
| `usiboy/testA` | `1404606559` | `b7dc40fd36558e7d952d287ab8dae843bbb4413e` | `06eb9696ea468930721f5b4f3eff2d90819ef3553385ca2a0a342b4585c0eaa7` | `vite-vue` |
| `usiboy/testB` | `1404606859` | `9ea1fc0c7037315d7bda12e5b34ca3fafec2a3af` | `044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7eaf5cd30d906a` | `vite-react` |

旧项目记录中的 tar SHA 不同：归档打包格式/元数据不同。这里验证的是**本项目重新产生的实际归档字节**，不能把不同打包器的 SHA 差异误称源 Git 文件变更。两个源码包暂存于本机专用 `0700` 临时目录，文件 `0600`，不纳入新仓库。

## ECS 构建现场与阻断

读-only 盘点：旧控制 ECS `/data` 剩余约 35 GiB，原 80/443、回环 18092/18093 仍占用；用户先前的另一台 ECS `i-bp18trmcosppv0ez0uze` 当时仍 Running、`/mnt/data` 剩余约 37 GiB、没有运行容器，只提供 `runc`（**无 gVisor 等强隔离运行时**）。在第二台 ECS 的**本轮独立 `0700` 目录**中，通过 Workbench 仅上传两份源码归档；Mac/ECS SHA-256 逐份相同。App 私钥和数据库凭据均未上传。Node 22 Bookworm 镜像按缓存 OCI 摘要 `sha256:dd9d21971ec4395903fa6143c2b9267d048ae01ca6d3ea96f16cb30df6187d94` 调用；没有独立上游签名审查。

- `testA`：Docker 只挂本轮源码/工作目录/经 npm 锁文件完整性校验的离线依赖缓存；`npm ci --offline --ignore-scripts` 后在 `--network none`、只读根、无额外 capability、禁提权、UID/GID 1000、`PID ≤ 128`、`CPU ≤ 3`、`内存 ≤ 6 GiB` 下 `vite build` **退出成功**，输出 `index.html`、CSS、JS 3 个文件，约 0.85 秒；新制品归档 SHA-256 `95b606560d3d47f90304dde1e830fe4e102bac5ebc022a3dd971a06ce0461dfd`。
- `testB` 默认配置：`npm ci --offline --ignore-scripts` 完成，Vite v7.1.2 一直停在 `transforming...`；低规格/高规格及移除 React 插件的限时试验均不能被记为构建完成。现场 Docker process CPU 约 115%、常驻约 1.3 GiB，主动终止后的容器为 `Exit=137`、`OOM=false`；Node Inspector 3 秒 CPU 样本中 Rollup `includePath`/`trackEntityAtPathAndGetIfTracked` 有持续活跃，提示 tree-shaking 路径是**诊断方向**，不以单份采样证明源码或插件根因。
- `testB` 受控对照：**不修改原 Git 提交**，保持 React 插件，临时配置 `build.rollupOptions.treeshake=false`；在同一 Bookworm 固定镜像、`--network none`、只读根、无额外 capability、禁提权、UID/GID 1000、`PID ≤ 128`、`CPU ≤ 2`、`内存 ≤ 3 GiB` 下，Vite **Exit=0、OOM=false、约 0.92 秒**，输出 3 个文件；JS 约 188.58 KiB（体积较大），新制品归档 SHA-256 `92d909ef69e684f09fcdd82ed88468e7181ce74a2fec5c69547db1818effe712`。这个诊断覆盖配置不作为所有 React 站点的默认生产构建策略；仍需真实浏览器验产物业务结果和供应链验证。

两份静态制品已下载到本机同一受保护临时目录，Mac/ECS SHA-256 分别一致。退出/强制终止的本轮 `ewp-build-*` 容器均已核对无运行残留；旧 ECS 入口与旧项目数据未变。

**此阶段边界：**这只是用户指定的固定可信公开样例在单独 ECS `runc` 中运行，不证明可以执行不可信租户 npm/Vite 插件或解决硬磁盘、PID 异常杀停、签名、构建缓存投毒和异机恢复等准入。App 轮询建立的两份来源仍是 `awaiting_isolated_build`；其后由操作员对已核验的 A/B 静态归档**另外**创建可信 fixture，并在旧控制 ECS 的新项目专用数据库和入口完成了私有 Preview，见[后续 ECS 实测](2026-10-06-ecs-private-preview-ab.md)。两条路径不能混为自动构建/正式上线；`hosts` 仅能为配置过的本机解析精确域名，不能代替 DNS/证书所有权。
