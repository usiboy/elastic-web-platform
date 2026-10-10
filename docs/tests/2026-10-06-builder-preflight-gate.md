# Git 构建前置准入：Vite 命令约束与独立隔离运行时缺失

**日期：2026-10-06。结论：前置校验已落地，平台自动 Vite 构建仍阻断。** 此次没有让控制 ECS、旧站点或本机 Docker 执行来自租户的构建脚本，也没有创建任一自动 Vite Deployment。

**后续增量：**另建的 Mac 专用实验 VM 已经校验官方 ARM64 归档并实际安装 `runsc`，testA/testB 在此 VM 的真正沙箱中重复构建通过，见[实测记录](2026-10-06-runsc-real-ab-handoff.md)。下文“Mac Docker 只有 runc”指此前默认 daemon，不代表该新建专用 VM；云端自动 Builder 仍未准入。

**2026-10-07 再更新：**用户允许原控制 ECS 试点 Builder；该机原共享 daemon 的 systemd cgroup 驱动与 gVisor 不兼容，但独立网络命名空间/cgroupfs 的第二 daemon 已实测用 `runsc` 构建 A/B 固定测试来源。详见[同机 Builder 与恢复记录](2026-10-07-original-ecs-builder-ab.md)。本段旧实例列表和 `runc` 探针仅是历史基线，不能覆盖新验证，也不能把同机试点当独立故障域的陌生租户上线准入。

## 现场资产核查

- `workbench list ecs`：杭州只见旧控制 ECS `i-bp19lwbwc5ej8pk0917s`；上海、北京没有可见实例。此前的构建 ECS `i-bp18trmcosppv0ez0uze` 尚未恢复可访问，不能借用旧控制机代替独立 Builder。
- Mac 的 `docker info --format '{{json .Runtimes}}'` 只报告 `runc`/`io.containerd.runc.v2`，不报告 `runsc`。`go run ./cmd/builder-probe` 以状态 `builder_runtime=blocked reason=isolation_unavailable` 拒绝，退出码 1；这个命令只读取运行时元数据，不执行源码。
- `internal/build.RequireRunsc` 检查 Docker 声明的 `runsc` runtime 及其可执行路径；缺失、伪装指向 `runc`、相对路径或格式错误一律失败。检测到 `runsc` **也不等于**建站放行：尚需独立节点归属/镜像摘要、cgroup 与磁盘/PID/时间限制、可信依赖缓存、签名产物及多租户反例。当前 `source-worker` 与该探针不联动，Vite 仍必须停在 `awaiting_isolated_build`。

## 源码合同与回归

`framework.Detect` 原先只检查 `scripts.build` **以** `vite build` 开头，因此 `vite build && curl ...`、`; env`、换行追加脚本或 `--config` 任意扩展都可能被误识别为受支持的 preset。新增失败用例先观察到四种误接收；随后收紧为仅支持修剪空白后的精确 `vite build`。返回给将来 Builder 的命令改为 `./node_modules/.bin/vite build`，避免 `npm run build` 自动执行用户的 `prebuild`/`postbuild` 钩子。真实 Vite 配置及插件依然可执行用户代码，所以只有独立沙箱验证通过后才能使用这个命令；无需对浏览器或旧站额外开放接口。

独立本机 PostgreSQL 下 `go test -race ./... -count=1` 全包通过，包含 Vite 不进入静态执行器、Source Worker 重试边界及新增拒绝用例。真实 GitHub App 只读轮询与取回两个原始仓库实际字节的 opt-in 测试通过：testA 固定 commit `e6062c7469d13712515dbf19474c851c044b753f`、testB 固定 commit `9ea1fc0c7037315d7bda12e5b34ca3fafec2a3af`，都保持 `origin=app_poll`、`status=awaiting_isolated_build`、`Deployment=0`；没有把 GitHub Actions 或操作员 fixture 改称平台 Builder。

## 下一次可解除门槛的条件

先恢复**独立** Builder 节点并批准 `runsc` 等强隔离运行时与固定镜像；控制节点、Mac `runc` 不可充当陌生租户的 Builder。建立最小 Builder 任务/来源签名后，使用真实 testA/testB 来源分别测离线依赖、CPU/内存/PID/磁盘/超时、失败产物不可发布和恶意依赖邻居隔离，再由网关读回真实 Preview。testB 默认 Rollup 构建此前未完成，不得把临时关闭 tree-shaking 的对照结果当作正式通过。完整依赖次序见[差距收敛设计](../plans/2026-10-06-r1-r2-gap-closure.md)。
