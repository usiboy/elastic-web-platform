#!/usr/bin/env bash
# 仅允许两份已核实的公开 Git 固定来源：在同 ECS 的独立 daemon 串行 runsc 构建。
# 输出采用 64 MiB 硬额 tmpfs；没有独立故障域/签名 Node ACK，仍只接受固定试点源码。
set -euo pipefail
umask 077

root=/data/ewp-builder-20261007
input=/data/ewp-builder-input-20261007
export DOCKER_HOST=unix:///run/ewp-builder-20261007/docker.sock
image=sha256:e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3
test "$(docker info --format '{{.CgroupDriver}}')" = cgroupfs
docker info --format '{{json .Runtimes}}' | python3 -c 'import json,sys; assert json.load(sys.stdin).get("runsc",{}).get("path")=="/usr/local/bin/runsc"'
test "$(docker image inspect "$image" --format '{{.Id}}')" = "$image"
exec 9>"$root/state/build.lock"
flock -n 9 || { printf 'builder_trial_busy\n' >&2; exit 1; }

case "${1:-}" in
  testA)
    archive="$input/testA-e6062c7469d1.tar.gz"
    source_sha=e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d
    cache=ewp-builder-a-cache-20261007
    output=ewp-builder-a-output-bounded-20261007
    cpu=1 memory=2g work=960m
    html=e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6
    css=e1e8f6dd498f7017c7033bf78947a1fa907ede06cedbe10de3de842cf0be1531
    js=78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a
    css_path=/out/dist/assets/index-NsIWs94z.css
    js_path=/out/dist/assets/index-DXuZjoRV.js
    ;;
  testB)
    archive="$input/testB-cfaa2f283f5c.tar.gz"
    source_sha=4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3
    cache=ewp-builder-b-cache-20261007
    output=ewp-builder-b-output-bounded-20261007
    cpu=2 memory=3g work=1920m
    html=b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a
    css=cee9771c708e34a72a0e3dde37cb41e83b0641928604a8f0151d59fbfc6f4e3c
    js=0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b
    css_path=/out/dist/assets/index-DMyhoa2j.css
    js_path=/out/dist/assets/index-DRp-iAjd.js
    ;;
  *) printf '仅支持 testA 或 testB 固定来源\n' >&2; exit 2 ;;
esac

test -f "$archive" && test ! -L "$archive"
test "$(sha256sum "$archive" | cut -d' ' -f1)" = "$source_sha"
docker volume inspect "$cache" >/dev/null
bash "$root/state/prepare_bounded_builder_output.sh" "$1"
# 限额挂载下旧 bundle 不得残留：新作业只可签刚构建的产物。
output_path=$(docker volume inspect "$output" --format '{{.Mountpoint}}')
test "$output_path" = "$root/docker/volumes/$output/_data"
rm -f "$output_path/bundle.tar.gz"
rm -rf "$output_path/dist"
if bash "$root/state/run_builder_deadline.sh" "$1" 100 \
  --rm --pull never --log-driver none --runtime runsc --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 \
  --memory "$memory" --memory-swap "$memory" --cpus "$cpu" \
  --tmpfs "/work:rw,exec,nosuid,nodev,size=$work" --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  --mount "type=bind,src=$archive,dst=/input/source.tar.gz,readonly" \
  --mount "type=volume,src=$cache,dst=/cache,readonly" \
  --mount "type=volume,src=$output,dst=/out" --env HOME=/work \
  --entrypoint sh "$image" -c \
  'mkdir /work/site && tar -xzf /input/source.tar.gz -C /work/site && cd /work/site && npm ci --offline --ignore-scripts --cache /cache --no-audit --no-fund && timeout 60 ./node_modules/.bin/vite build --outDir /out/dist --emptyOutDir && tar -czf /out/bundle.tar.gz -C /out/dist .'; then
  :
else
  # 失败路径丢弃可能已写完的归档，不能凭 mtime 把半途失败的作业签名。
  rm -f "$output_path/bundle.tar.gz"
  exit 1
fi

actual_html=$(docker run --rm --pull never --runtime runsc --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 --memory 256m --mount "type=volume,src=$output,dst=/out,readonly" --entrypoint sha256sum "$image" /out/dist/index.html | cut -d' ' -f1)
actual_css=$(docker run --rm --pull never --runtime runsc --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 --memory 256m --mount "type=volume,src=$output,dst=/out,readonly" --entrypoint sha256sum "$image" "$css_path" | cut -d' ' -f1)
actual_js=$(docker run --rm --pull never --runtime runsc --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 1000:1000 --pids-limit 64 --memory 256m --mount "type=volume,src=$output,dst=/out,readonly" --entrypoint sha256sum "$image" "$js_path" | cut -d' ' -f1)
test "$actual_html" = "$html" && test "$actual_css" = "$css" && test "$actual_js" = "$js"
printf 'fixed_git=%s runtime=runsc source_sha256=%s html_sha256=%s css_sha256=%s js_sha256=%s\n' "$1" "$source_sha" "$html" "$css" "$js"
