#!/usr/bin/env bash
# 仅在原 ECS 已有的官方 x86_64 gVisor 二进制上注册 Docker runsc runtime。
# 先保留共享 daemon 配置和旧容器 ID；失败时只恢复配置并 reload，不重启旧服务。
set -euo pipefail

root=/data/ewp-builder-20261007
backup="$root/state/daemon.json.pre-runsc"
test -s "$backup"
test "$(sha256sum "$backup" | cut -d' ' -f1)" = 2e891b029c6cdd9316146a11821194f6d3097f37b151158235315b716b24e9c7
test "$(sha256sum /etc/docker/daemon.json | cut -d' ' -f1)" = 2e891b029c6cdd9316146a11821194f6d3097f37b151158235315b716b24e9c7
test "$(sha256sum /usr/local/bin/runsc | cut -d' ' -f1)" = 88a87d5d6d06160d4c144f51f95ec6dd6fe0fc6b02697eb877876683c5b57a91
test "$(sha256sum /usr/local/bin/gvisor-bin/gvisor_sentry | cut -d' ' -f1)" = e10e6e8072a0822f5e80dc203d156f0602b15fab90b85fac1d7bffd79477b267
test "$(sha256sum /usr/local/bin/containerd-shim-runsc-v1 | cut -d' ' -f1)" = 2f78211c0726d5dc80601b3eff39a071839a19eeb2f3e43c013e13edebb44713
test ! -e /etc/docker/.daemon.json.ewp-restore
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_app=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)

committed=false
restore() {
  if [ "$committed" = false ]; then
    cp -p "$backup" /etc/docker/.daemon.json.ewp-restore
    mv -f /etc/docker/.daemon.json.ewp-restore /etc/docker/daemon.json
    systemctl reload docker || true
  fi
}
trap restore EXIT

/usr/local/bin/runsc install
systemctl reload docker
docker info --format '{{json .Runtimes}}' | python3 -c 'import json,sys; assert json.load(sys.stdin).get("runsc",{}).get("path")=="/usr/local/bin/runsc"'
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_app"
test "$(docker inspect --format '{{.State.Running}}' ewp-postgres-preview-20261006)" = true
test "$(docker inspect --format '{{.State.Running}}' xianlixiu-reviewed-empty-poc)" = true
test "$(curl --noproxy '*' --silent --show-error -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
for unit in nginx ewp-preview-api-20261006.service ewp-preview-site-20261006.service; do
  systemctl is-active --quiet "$unit"
done
committed=true
printf 'runsc_registered=verified legacy_ip=401 original_containers=unchanged\n'
