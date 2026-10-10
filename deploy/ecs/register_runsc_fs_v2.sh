#!/usr/bin/env bash
# 只给已注册的 runsc 添加显式 cgroupfs 参数；不重启共享 Docker daemon。
# 宿主 systemd 239 无法运行 gVisor systemd cgroup 驱动，失败立即恢复原配置。
set -euo pipefail

root=/data/ewp-builder-20261007
backup="$root/state/daemon.json.runsc-only"
test -s "$backup"
test "$(sha256sum "$backup" | cut -d' ' -f1)" = 376f7eb5fe2173dbd912c75f0791f333995b342eccc106d6bdd62cb9a040f902
test "$(sha256sum /etc/docker/daemon.json | cut -d' ' -f1)" = 376f7eb5fe2173dbd912c75f0791f333995b342eccc106d6bdd62cb9a040f902
test ! -e /etc/docker/.ewp-builder-fs-v2-restore
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_app=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)

committed=false
restore() {
  if [ "$committed" = false ]; then
    cp -p "$backup" /etc/docker/.ewp-builder-fs-v2-restore
    mv -f /etc/docker/.ewp-builder-fs-v2-restore /etc/docker/daemon.json
    systemctl reload docker || true
  fi
}
trap restore EXIT

python3 - <<'PY'
import json
import os
import stat
import tempfile

name = '/etc/docker/daemon.json'
with open(name, encoding='utf-8') as source:
    config = json.load(source)
if config.get('runtimes', {}).get('runsc') != {'path': '/usr/local/bin/runsc'} or config.get('default-runtime') not in (None, 'runc'):
    raise SystemExit('unreviewed daemon runtime configuration')
config['runtimes']['runsc']['runtimeArgs'] = ['--systemd-cgroup=false']
fd, temporary = tempfile.mkstemp(prefix='.ewp-builder-fs-v2-', dir='/etc/docker')
try:
    os.fchmod(fd, stat.S_IMODE(os.stat(name).st_mode))
    with os.fdopen(fd, 'w', encoding='utf-8') as out:
        json.dump(config, out, ensure_ascii=False, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())
    os.replace(temporary, name)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
systemctl reload docker
docker run --rm --pull never --runtime runsc --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 --memory 256m --memory-swap 256m --cpus 1 \
  sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3 node -e 'console.log("ecs_runsc_cgroupfs_smoke=ok")'
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_app"
test "$(curl --noproxy '*' --silent --show-error -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
committed=true
printf 'runsc_cgroupfs_verified=1 legacy_ip=401 original_containers=unchanged\n'
