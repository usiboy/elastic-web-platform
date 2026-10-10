# 真正 `runsc` 沙箱下 testA/testB 构建与本机签名作业交接

**日期：2026-10-06。验收范围：用户授权的两个公开测试仓库、Mac 上独立实验 VM、本机隔离 PostgreSQL。** 两个 Repo 的固定 Git 提交均在实际 gVisor `runsc` 容器中**重复构建成功**，静态 HTML/CSS/JS 与各自 GitHub Actions 制品逐字节一致；这些输出进入了本机一次性作业/签名交接代码合同。控制 ECS 未安装 gVisor、未应用迁移 017/018、未接入在线自动 Builder，旧站及操作员 fixture 路径保持独立。

## 独立 VM 与运行时真实性

新建独立 Colima `ewp-builder-20261006`（Linux ARM64，3 CPU/6 GiB/28 GiB，Kernel `6.8.0-64-generic`，无 Kubernetes/SSH Agent 转发；只读挂载两份公开测试源码目录）。保留 Mac 默认 Colima VM 与旧 ECS 未改动。gVisor 官方 [`release-20260928.0`](https://gvisor.dev/docs/user_guide/install/) ARM64 包经官方 GCS 与 GitHub 发布资源镜像分段取得，**只在最终 SHA-512 `31519c2c8476c6eb24f51aa1378e555b4e86883dc1c8d81fb82df1af1900f38b10149affaa05bcc2418e57989bd04a33603a10ac73fee92e0fe20aef44e402e3` 与官方校验文件完全一致后**使用，不以镜像站名充当信任证明；包内检查仅为 `runsc`、配套 `gvisor-bin/` 和 shim。在**独立 VM 内**安装 `runsc` 并 reload 该 VM Docker，`runsc --version` 实际打印 `release-20260928.0`、平台只读 `builder-probe` 发现运行时、实际 Node 22.22.0 容器以 `--runtime runsc` 启动成功；Docker Inspect 回读 `Runtime=runsc`、`NetworkMode=none`、只读根、内存 268435456 字节与 `PidsLimit=32`。原默认 Docker daemon 与旧 ECS 未注册这个 runtime。

由于 VM 的只读 Mac 挂载目录为 `0700/0600`，gVisor gofer 无法遍历，不得降低 GitHub App PEM 的保护权限。只将**已在 Mac 逐字节验证且不含凭据**的两份公开源码 tar.gz 复制进独立 VM `/srv/ewp-builder-input/`，文件摘要仍为下表的来源值；App 私钥不入 VM、任何构建容器或 ECS。镜像是独立 VM 中已核固定 `node:22.22.0-alpine` config SHA-256 `e4bf2a82ad0a4037d28035ae71529873c069b13eb0455466ae0bc13363826e34`。A/B 分别在无平台密钥、`--ignore-scripts` 的准备容器填充独立 npm 缓存，真正构建阶段只读挂载各自缓存，`npm ci --offline --ignore-scripts`，直接调用 Vite，`--network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 501:501 --pids-limit 64 --memory 3g --memory-swap 3g --cpus 2`；工作区为 2 GiB tmpfs、`/tmp` 为 64 MiB tmpfs，容器超时 60 秒。**输出使用 Docker 命名卷，尚无针对输出卷的硬磁盘额；这是后续线上准入阻断。**

## 固定 Git 与真实静态字节

| 测试 Repo | 实际 App 固定 commit / 来源包 SHA-256 | `runsc` 内成功构建 | 真实归档 SHA-256 | HTML / JS SHA-256 |
|---|---|---|---|---|
| testA Vue / 纤莉秀公开模板 | `e6062c7469d13712515dbf19474c851c044b753f` / `e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d` | Vite 10 modules；两次约 0.66～0.70 秒 | `51fb8db4845b6a8f312ffba5de75e31a76f6540c64fab3b66009759c98c9ea55` | `e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6` / `78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a` |
| testB React 专属修复 | `cfaa2f283f5ced67b85899919fe10a4a9f99d014` / `4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3` | Vite 28 modules；两次约 0.65～0.67 秒 | `19de65abbd55d12bf6eea3fdbe15baef5256aa9200b340021f89893aaa20f967` | `b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a` / `0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b` |

两次重建的内容 SHA 未变，分别与 testA 既有成功 CI 与 testB [Actions run `37475046275`](https://github.com/usiboy/testB/actions/runs/37475046275) 的 HTML/CSS/JS 全部一致；gzip tar 时间戳不同，不将归档外壳 SHA 差异误称文件内容漂移。缺 `runsc` 时专用 Builder 探针曾按预期拒绝；限制为 `96 MiB` 内存时 `runsc` 测试容器启动即报告 Out of memory，PID 上限 32 的探针大量派生子进程出现拒绝，之后 B 产物仍可按摘要读回且没有运行中的测试容器。上述负例**不能区分 PID/内存混合限制的每个因果，也尚未验收磁盘配额、敌意依赖和多租户邻居压测**。

## 本机控制面交接结果

`TestLivePinnedABGVisorOutputsEnterPrivateCandidatesOnly` 在 Mac 隔离 PostgreSQL 中使用真正 GitHub App（单仓库只读令牌）轮询 testA/testB 当前固定 SHA，Source Worker 逐一核验 Git 源并保持 `awaiting_isolated_build`；然后为各 Team/Project 登记**测试逻辑预算**和一次性作业，读取上述已确认的真实 `runsc` 归档。使用**测试内临时 Ed25519 密钥**调用 `SignBuild`/`AcceptSignedBuild`，逐项核对实际来源、archive SHA、CI HTML/JS 字节、作业 nonce、私有 Host；两份真实归档在本机各变成一个 `preview_candidate`，互换 A/B 产物被拒绝、同一签名重放不新增 Deployment、没有 `preview_ready` 观察态或生产 Alias。测试结束时独立 schema 自动删除，**测试密钥不入 VM/ECS，也没有被当作受控 Builder 生产私钥**。

ECS 上新 B 私有站点仍是经真实 CI/独立 VM 字节复核的**操作员 fixture**，Host `b-cfaa2f2.preview.ecp.lingtong.com`；其 HTTPS 会话与旧 B/旧 IP 401 回归见[前段记录](2026-10-06-real-git-ab-ci-independent-vm.md)。专用 VM 已证明两份源码在 gVisor 下能成功构建，**尚未证明平台能在线发作业、在受控 Builder 保存签名私钥、计量总磁盘额、让 Gateway 自动读回签名候选和完成生产切流**。正式 ECS 第二 Builder 仍不可见，Mac 试验 VM 不能冒充持续运行的租户 SaaS 节点。验证结束后仅停止新建的 `ewp-builder-20261006` VM，保留其已核验运行时、A/B 缓存与产物卷供后续准入复验；未影响默认 Colima VM、旧 ECS 或站点。ECS 新平台仍为迁移 016、匿名控制 API 401。
