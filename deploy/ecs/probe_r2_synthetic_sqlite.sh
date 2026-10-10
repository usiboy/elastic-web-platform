#!/usr/bin/env bash
# 仅合成数据：原 ECS runsc Node + SQLite 单写者/在线快照/恢复反例。
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
name=ewp-r2-synth-writer-20261007
volume=ewp-r2-synth-sqlite-20261007
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
root=/data/ewp-builder-20261007/docker/volumes
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
test -z "$(docker ps -aq --filter "name=^/${name}$")"
test -z "$(docker volume ls -q --filter "name=^${volume}$")"
bridge=$(< /sys/class/net/docker0/ifindex)
old_db=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)
old_site=$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)
path=''
created=false
mounted=false
started=false
cleanup() {
  if [ "$started" = true ]; then timeout -k 2s 10s docker rm -f "$name" >/dev/null 2>&1 || true; fi
  if [ "$mounted" = true ]; then umount "$path" || return 1; fi
  if [ "$created" = true ]; then docker volume rm "$volume" >/dev/null || return 1; fi
}
trap cleanup EXIT
docker volume create "$volume" >/dev/null
created=true
path=$(docker volume inspect "$volume" --format '{{.Mountpoint}}')
test "$path" = "$root/$volume/_data" && test -d "$path" && test ! -L "$path" && ! mountpoint -q "$path"
mount -t tmpfs -o size=32m,nosuid,nodev,noexec,mode=0700,uid=1000,gid=1000 tmpfs "$path"
mounted=true
test "$(findmnt -n -o SIZE -M "$path" | tr -d ' ')" = 32M

started=true
docker run --rm -d --pull never --name "$name" --log-driver none --runtime runsc \
  --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user 1000:1000 --pids-limit 32 --memory 256m --memory-swap 256m --cpus 0.5 \
  --mount "type=volume,src=$volume,dst=/db" --tmpfs /tmp:rw,nosuid,nodev,noexec,size=8m \
  --entrypoint node "$image" --experimental-sqlite -e '
const fs=require("node:fs");
const {DatabaseSync}=require("node:sqlite");
const lock="/db/demo.sqlite.writer.lock";
const fd=fs.openSync(lock,"wx",0o600);
const db=new DatabaseSync("/db/demo.sqlite");
db.exec("CREATE TABLE demo (id TEXT PRIMARY KEY)");
db.prepare("INSERT INTO demo VALUES (?)").run("DEMO-ONLY");
process.on("SIGTERM",()=>{db.close();fs.unlinkSync(lock);fs.closeSync(fd);process.exit(0)});
setInterval(()=>{},1000);
' >/dev/null
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if test -f "$path/demo.sqlite.writer.lock" && test "$(docker inspect "$name" --format '{{.State.Running}}')" = true; then ready=true; break; fi
  sleep 0.2
done
test "$ready" = true

docker run --rm --pull never --log-driver none --runtime runsc --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 1000:1000 --pids-limit 32 --memory 256m --memory-swap 256m --cpus 0.5 \
  --mount "type=volume,src=$volume,dst=/db" \
  --entrypoint node "$image" -e '
const fs=require("node:fs");
try { fs.openSync("/db/demo.sqlite.writer.lock","wx"); process.exit(1) }
catch(error) { if(error.code!=="EEXIST") throw error; console.log("second_writer=denied") }
'
docker exec "$name" node --experimental-sqlite -e '
const {DatabaseSync,backup}=require("node:sqlite");
(async()=>{const source=new DatabaseSync("/db/demo.sqlite",{readOnly:true});
await backup(source,"/db/snapshot.sqlite");source.close();
const copy=new DatabaseSync("/db/snapshot.sqlite",{readOnly:true});
if(copy.prepare("PRAGMA integrity_check").get().integrity_check!=="ok" || copy.prepare("SELECT count(*) AS n FROM demo").get().n!==1)process.exit(1);
copy.close();console.log("synthetic_snapshot=verified")})().catch(()=>process.exit(1));
'
docker stop -t 5 "$name" >/dev/null
started=false
test ! -e "$path/demo.sqlite.writer.lock"
docker run --rm --pull never --log-driver none --runtime runsc --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 1000:1000 --pids-limit 32 --memory 256m --memory-swap 256m --cpus 0.5 \
  --mount "type=volume,src=$volume,dst=/db" \
  --entrypoint node "$image" --experimental-sqlite -e '
const fs=require("node:fs");const {DatabaseSync}=require("node:sqlite");
fs.copyFileSync("/db/snapshot.sqlite","/db/restored.sqlite",fs.constants.COPYFILE_EXCL);
const fd=fs.openSync("/db/restored.sqlite.writer.lock","wx",0o600);
const db=new DatabaseSync("/db/restored.sqlite");
if(db.prepare("PRAGMA integrity_check").get().integrity_check!=="ok" || db.prepare("SELECT id FROM demo").get().id!=="DEMO-ONLY")process.exit(1);
db.close();fs.unlinkSync("/db/restored.sqlite.writer.lock");fs.closeSync(fd);
console.log("synthetic_restore=verified");
'
test "$(< /sys/class/net/docker0/ifindex)" = "$bridge"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' ewp-postgres-preview-20261006)" = "$old_db"
test "$(docker --host unix:///var/run/docker.sock inspect --format '{{.Id}}' xianlixiu-reviewed-empty-poc)" = "$old_site"
printf 'r2_sqlite_synthetic=verified old_site_unchanged=1\n'
