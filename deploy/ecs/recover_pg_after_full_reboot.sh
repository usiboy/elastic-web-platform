#!/usr/bin/env bash
# Recover only the new website platform PostgreSQL after the root/cap-drop restart failure.
# Original container and the verified pg_dump are retained; no PGDATA copy/delete.
set -euo pipefail
umask 077
root=/data/ewp-preview-20261006
name=ewp-postgres-preview-20261006
retained=ewp-postgres-preview-20261006.pre-uid70-recovery-20261008
image=docker.1ms.run/library/postgres@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea
expected_id=7085d10db80ff59ed3e8bacc735ac39a8b730d5f7cbf6d9723df8df633237735
test "$(sha256sum "$root/state/pre-full-reboot-20261008.dump" | cut -d' ' -f1)" = a88bda16a58c638874f1d46283dc2c3c1264e936aaa407cb69addaaba66670a8
pg_restore --list "$root/state/pre-full-reboot-20261008.dump" >/dev/null
test ! -e "$root/state/postgres/postmaster.pid" || test "$(docker inspect "$name" --format '{{.State.Restarting}}')" = true
test "$(docker inspect "$name" --format '{{.Id}}')" = "$expected_id"
test "$(docker inspect "$name" --format '{{.Config.Image}}')" = "$image"
test ! "$(docker ps -aq --filter "name=^/${retained}$")"
test "$(stat -c '%a:%u' "$root/state/postgres")" = 700:70
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
old_site=$(docker inspect xianlixiu-reviewed-empty-poc --format '{{.Id}}')
docker update --restart=no "$name" >/dev/null
stopped=false
for attempt in {1..60}; do
  if [ "$(docker inspect "$name" --format '{{.State.Running}}')" = false ]; then stopped=true; break; fi
  sleep 1
done
test "$stopped" = true
docker rename "$name" "$retained"
test "$(docker inspect "$retained" --format '{{.Id}}')" = "$expected_id"

docker run -d --pull never --name "$name" --restart unless-stopped --user 70:70 \
  --cap-drop ALL --security-opt no-new-privileges --pids-limit 128 \
  --memory 768m --memory-swap 768m --cpus 1 \
  --network bridge --publish 127.0.0.1:15434:5432 \
  --mount "type=bind,src=$root/state/postgres,dst=/var/lib/postgresql/data" \
  --tmpfs /var/run/postgresql:rw,nosuid,nodev,size=16m,uid=70,gid=70,mode=0770 \
  --env PGDATA=/var/lib/postgresql/data "$image" >/dev/null
new_id=$(docker inspect "$name" --format '{{.Id}}')
test "$new_id" != "$expected_id"
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.5
done
test "$ready" = true
set -a
source "$root/private/site.env"
set +a
test "$(psql "$DATABASE_URL" -Atc 'SELECT count(*) FROM schema_migrations')" = 20
test "$(docker inspect "$retained" --format '{{.State.Running}}')" = false
test "$(docker inspect xianlixiu-reviewed-empty-poc --format '{{.Id}}')" = "$old_site"
test "$(curl --noproxy '*' --silent --max-time 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:13000/api/live)" = 200
printf 'new_platform_postgres=ready uid=70 migrations=20 retained_old_container=%s legacy=200\n' "$retained"
