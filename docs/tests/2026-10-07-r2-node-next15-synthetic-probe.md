# R2 动态运行前置实测：Node 22 与 Next.js 15.5.27 合成样例

**日期：2026-10-07。结论：**真实 `runsc` 已在原 ECS 的独立 Builder daemon 中运行无凭据 Node 22 HTTP 探针和固定 Next.js 15.5.27 standalone 样例。Next SSR 每次请求生成不同内容，API、分块响应在隔离内部网络读回；测试后容器和一次性网络均删除。**这是短时运行时可行性证据，不是 R2 平台发布、动态业务迁移或 SaaS 准入。**

## 固定版本与输入边界

- 合成 fixture 位于 `deploy/runtime/next15-fixture/`，依赖精确锁定 `next=15.5.27`、`react=19.1.1`、`react-dom=19.1.1`，Node 为现有镜像的 `22.22.0`。`outputFileTracingRoot` 指向 fixture 本身，避免 Next 自动推断更高层工作区并误把别的目录打入 standalone。没有快麦、SQLite、真实订单、业务访问 Token、平台 App 私钥或数据库连接。
- 本机 `npm ci --ignore-scripts --no-audit --no-fund`、`npm run build`、`npm run test:runtime` 通过；Next 构建报告 `/`、`/api/hello`、`/api/stream` 为按需动态路由。本机真实 HTTP 测试验证两次 SSR 请求 UUID 不同，JSON API 返回 Node 版本，静态 JS 可读取，响应体分两段传输。standalone 只打包 fixture 输出，2331 个条目无符号链接/路径逃逸，15 MiB 压缩包 SHA-256 `d35c46dc9b64df6fef0932cc1d96f4aac283c4ab1b1ccd9ae7f52212a356ec89`。

## ECS 受限运行与失败回收

先用 `probe_r2_node_runtime.sh` 在 `--network none`、只读根、无 capability、禁提权、UID 1000、PID ≤32、CPU 0.5、内存 256 MiB、仅 `/tmp=16 MiB` 的真实 `runsc` 容器中验证 Node HTTP 200、流式两段、404；向挂住的请求读取正文，宿主两秒超时返回 124，随后强制删除该合成容器，原 ECS 两个旧容器与宿主 `docker0` 均未改变。脚本 SHA-256 `1c6298d80eb06c29e2460e5a9423f3e8bd3493bbcead47ad880d2bdded946fb3`。

Next standalone 从受核摘要的归档解到 Builder 独立目录，作为**只读** `/app` 挂入 `runsc`，CPU 1、内存 768 MiB、PID ≤64、只读根、无 capability、禁提权、UID 1000、`/tmp=32 MiB`。独立 daemon 的 `PrivateNetwork=yes` 与宿主网络 inode 不同。`--network host` 在该 `runsc` 模式内没有可绑定接口，先前启动报 `EADDRNOTAVAIL`，未当作成功；改为只在**daemon 自己的 netns** 中创建一次性 `--internal` 桥 `172.29.77.0/24`，从同一 netns 对容器 IP 读回：`/api/hello=200`、两次 SSR UUID 不同、`/api/stream` 两段间隔达到 50 ms。探针退出前删除容器与测试网络，宿主 `docker0`、原 PostgreSQL 和旧站容器 ID 未变；工作台/Preview/原 HTTP 静态站/旧 IP 401 的四组 ECS opt-in `go test -race` 回归通过。最终脚本 `probe_r2_next15_runtime.sh` SHA-256 `827edb8a47b576723cae0b377934dc5e765a3f566317b607e0c55989cd4b6138`。

诊断中 `local` Docker 日志驱动设置 `max-file=1` 而不关闭压缩时曾拒绝创建任务、留下单个 `Created` 探针容器；核验其名称/状态/镜像后仅清理该容器，脚本加入 `compress=false` 和失败清理。为排障曾启用单份 1 MiB 有界日志；没有永久调试端口、额外公网 Host 或持续运行进程。

## 仍需完成的 R2 准入

目前 Next 服务只在独立 daemon 的内部网络中由 root 验证，**没有**平台部署模型中的动态 Bundle/函数路由、网关鉴权与流式代理、热实例池/缩零、并发与 DB 连接上限、每请求超时后的实例杀停和自动重建、持久 Secret/SQLite 单写者及快照恢复。Next 的合成成功不证明纤莉秀原 Express/`kuaimai-cli` 动态业务可托管。用户允许未来移除原 ECS 旧站，但在替代路径和回退材料齐备前它仍保持运行；用户未提供公网 DNS/受信证书，而当前 Builder/控制/运行也共享同一 ECS 故障域。R1 尚 blocked，R2 亦 blocked；不能将本试验视为正式 R2 完成。
