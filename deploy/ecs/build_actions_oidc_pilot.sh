#!/usr/bin/env bash
# 仅 A/B OIDC 已核源作业：独立 netns daemon 内 runsc，固定离线缓存和 64 MiB 输出硬限。
set -euo pipefail
umask 077
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
root=/data/ewp-builder-20261007
source_store=/data/ewp-preview-20261006/sources
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
case "${1:-}" in
  testA)
    team=t_f9cf963718a72612c48940af8a8296e4
    project=p_34401f5c05af1079010cb95ee3ec1275
    cache=ewp-builder-a-cache-20261007
    output=ewp-builder-a-output-bounded-20261007
    cpu=1 memory=2g work=960m ;;
  testB)
    team=t_eee8046ca49c271f5bea06acf285be66
    project=p_e5123bae12388454bb82fc1590750ab9
    cache=ewp-builder-b-cache-20261007
    output=ewp-builder-b-output-bounded-20261007
    cpu=2 memory=3g work=1920m ;;
  *) printf 'unsupported OIDC pilot project\n' >&2; exit 2 ;;
esac
source_sha=${2:-}
[[ "$source_sha" =~ ^[0-9a-f]{64}$ ]] || exit 2
test "$(systemctl show ewp-builder-dockerd-20261007.service -p PrivateNetwork --value)" = yes
test "$(systemctl is-active ewp-builder-dockerd-20261007.service)" = active
test "$(docker info --format '{{.CgroupDriver}}')" = cgroupfs
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
docker info --format '{{json .Runtimes}}' | python3 -c 'import json,sys; assert json.load(sys.stdin).get("runsc",{}).get("path")=="/usr/local/bin/runsc"'
archive="$source_store/$team/$project/$source_sha"
test -f "$archive" && test ! -L "$archive"
test "$(sha256sum "$archive" | cut -d' ' -f1)" = "$source_sha"
exec 9>"$root/state/build.lock"
flock -n 9 || { printf 'isolated_builder_busy\n' >&2; exit 75; }
bash "$root/state/prepare_bounded_builder_output.sh" "$1" >/dev/null
docker volume inspect "$cache" >/dev/null
output_path=$(docker volume inspect "$output" --format '{{.Mountpoint}}')
test "$output_path" = "$root/docker/volumes/$output/_data"
test "$(findmnt -n -o FSTYPE -M "$output_path")" = tmpfs
test "$(findmnt -n -o SIZE -M "$output_path" | tr -d ' ')" = 64M
staged="$root/state/oidc-$1-source.tar.gz"
test ! -e "$staged"
cleanup() { rm -f "$staged" "$output_path/bundle.tar.gz"; rm -rf "$output_path/dist"; }
trap cleanup EXIT
cp "$archive" "$staged"
chmod 0644 "$staged"
test "$(sha256sum "$staged" | cut -d' ' -f1)" = "$source_sha"
rm -f "$output_path/bundle.tar.gz"
rm -rf "$output_path/dist"
bash "$root/state/run_builder_deadline.sh" "$1" 100 \
  --rm --pull never --log-driver none --runtime runsc --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 \
  --memory "$memory" --memory-swap "$memory" --cpus "$cpu" \
  --tmpfs "/work:rw,exec,nosuid,nodev,size=$work" --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  --mount "type=bind,src=$staged,dst=/input/source.tar.gz,readonly" \
  --mount "type=volume,src=$cache,dst=/cache,readonly" \
  --mount "type=volume,src=$output,dst=/out" --env HOME=/work \
  --entrypoint sh "$image" -c \
  'mkdir /work/site && tar -xzf /input/source.tar.gz -C /work/site && cd /work/site && npm ci --offline --ignore-scripts --cache /cache --no-audit --no-fund && timeout 60 ./node_modules/.bin/vite build --outDir /out/dist --emptyOutDir && tar -czf /out/bundle.tar.gz -C /out/dist .'
test -f "$output_path/bundle.tar.gz" && test ! -L "$output_path/bundle.tar.gz"
test "$(stat -c '%s' "$output_path/bundle.tar.gz")" -le 16777216
# 归档只复制到私有、按作业命名的新路径；不是从旧产物卷签名。
target=${3:-}
[[ "$target" =~ ^/data/ewp-builder-20261007/state/oidc-(testA|testB)-[a-f0-9]{32}[.]tar[.]gz$ ]] || exit 2
test ! -e "$target"
cp --no-clobber "$output_path/bundle.tar.gz" "$target"
chmod 0600 "$target"
printf 'oidc_builder_output=%s sha256=%s\n' "$1" "$(sha256sum "$target" | cut -d' ' -f1)"
