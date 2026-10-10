#!/usr/bin/env bash
# 在同机试点增加独立的 gVisor cgroupfs runtime 别名，保留默认 runc/systemd 驱动。
# 不更新宿主 systemd；失败后原子恢复共享 Docker 配置并 reload。
set -euo pipefail

root=/data/ewp-builder-20261007
backup="$root/state/daemon.json.runsc-only"
test ! -e "$backup"
test ! -e /etc/docker/.ewp-builder-fs-restore
test "$(sha256sum /etc/docker/daemon.json | cut -d' ' -f1)" = 376f7eb5fe2173dbd912c75f0791f333995b342eccc106d6bdd62cb9a040f902
old_db=$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_app=$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
cp -p /etc/docker/daemon.json "$backup"

committed=false
restore() {
  if [ "$committed" = false ]; then
    cp -p "$backup" /etc/docker/.ewp-builder-fs-restore
    mv -f /etc/docker/.ewp-builder-fs-restore /etc/docker/daemon.json
    systemctl reload docker || true
  fi
}
trap restore EXIT

python3 - <<'PY'
import json
import os
import stat
import tempfile

config_path = '/etc/docker/daemon.json'
with open(config_path, encoding='utf-8') as source:
    config = json.load(source)
if config.get('default-runtime') not in (None, 'runc') or config.get('runtimes', {}).get('runsc-fs'):
    raise SystemExit('Docker default/runtime already differs')
if config.get('runtimes', {}).get('runsc', {}).get('path') != '/usr/local/bin/runsc':
    raise SystemExit('official runsc runtime missing')
config['runtimes']['runsc-fs'] = {'path': '/usr/local/bin/runsc', 'runtimeArgs': ['--systemd-cgroup=false']}
fd, temporary = tempfile.mkstemp(prefix='.ewp-builder-fs-', dir='/etc/docker')
try:
    os.fchmod(fd, stat.S_IMODE(os.stat(config_path).st_mode))
    with os.fdopen(fd, 'w', encoding='utf-8') as out:
        json.dump(config, out, ensure_ascii=False, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())
    os.replace(temporary, config_path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
systemctl reload docker
docker info --format '{{json .Runtimes}}' | python3 -c 'import json,sys; r=json.load(sys.stdin); assert r.get("runsc-fs",{}).get("path")=="/usr/local/bin/runsc"'
docker run --rm --pull never --runtime runsc-fs --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 --memory 256m --memory-swap 256m --cpus 1 \
  sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3 node -e 'console.log("ecs_runsc_fs_smoke=ok")'
test "$(docker inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_app"
test "$(curl --noproxy '*' --silent --show-error -o /dev/null -w '%{http_code}' https://114.55.237.62/)" = 401
committed=true
printf 'runsc_fs_registered=verified legacy_ip=401 original_containers=unchanged\n'
