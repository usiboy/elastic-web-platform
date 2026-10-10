# 原纤莉秀应用合成实例：受控运行与退出

只涉及 Builder 第二 daemon 内的 `ewp-r2-xianlixiu-synthetic-20261008` 与 root-only Socket `/run/ewp-r2-20261007/xianlixiu.sock`。`ewp-r2-xianlixiu-synthetic-20261008.service` 和 `ewp-r2-xianlixiu-relay-persistent-20261008.service` 均由 `/etc/systemd/system` 加载并启用。旧 `xianlixiu-reviewed-empty-poc` 容器、默认 Docker daemon、R2 Next Stage 与 A 原 HTTP 生产域均非该实例。

源码包只允许使用 SHA-256 `afaf12b90deb1c89fe9ee7cbeea6ba15462bebefa3e4df32aae5797088d1eb25` 的无凭据合成归档；在该模式必须有 `EWP_SYNTHETIC_ONLY=1`，真实快麦 accessToken 登录返回 403，不能为跑通测试关闭限制。容器挂载的 `/data/ewp-builder-20261007/state/r2-xianlixiu-synthetic-data/` 只包含合成库/临时私有会话及其合成快照，不与旧站或本机真实 `db/product-pallet.sqlite` 混用。

日常只读检查：核两个持久 unit active/enabled、Builder `PrivateNetwork=yes`、合成容器 `runsc`/UID1000/资源限制、relay `/api/health` 返回 ready；经内部 CA 验证新固定 Preview 匿名401、A Owner 有效会话下首页与健康200、登录合成环境403、旧 Next Stage200及原 IP401。异常 SIGKILL 若留下 `synthetic.sqlite.writer.lock`，恢复脚本会拒绝另起第二写者；先确认原容器已停和备份完整，再人工评估，不自动抢锁或覆盖库。

只停/启合成服务：先停 `ewp-r2-xianlixiu-relay-persistent-20261008.service`，只停 `ewp-r2-xianlixiu-synthetic-20261008` 容器以释放锁；`systemctl restart ewp-r2-xianlixiu-synthetic-20261008.service` 会验证原归档、既有合成库/容器身份并在健康后复用，接着启动合成 relay 并再次读回固定私有 Host。现场脚本 `rehearse_r2_xianlixiu_restart.sh` 已验证 ID/快照不变。若需退出试点，先在控制库撤销该固定 Host/Deployment 的路由意图，再停 relay 和容器；不要顺手停共享 internal bridge 或旧 Next 实例。用户明确只允许合成数据：不做真实库/快麦会话迁入，也不因本轮成功下线原 ECS 受限探针。
