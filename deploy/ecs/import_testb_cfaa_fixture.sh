#!/usr/bin/env bash
# 仅为授权的测试租户 B 导入真实 GitHub CI/独立 VM 字节一致的操作员 fixture。
# 这不是签名 Builder 的自动 Deployment；保留旧 b.preview Host，不晋升生产。
set -euo pipefail

root=/data/ewp-preview-20261006
source_file="$root/state/fixture-inputs/repo-1404606859-cfaa2f283f5c.tar.gz"
built_file="$root/state/fixture-inputs/testB-ci-cfaa2f2.tar.gz"
test -f "$source_file" && test ! -L "$source_file"
test -f "$built_file" && test ! -L "$built_file"
test "$(sha256sum "$source_file" | cut -d' ' -f1)" = 4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3
test "$(sha256sum "$built_file" | cut -d' ' -f1)" = 84185759f85cb3e90756642f0f1d63efef87cf2db3168306588bbb7774cac78b

"$root/bin/ewp-operator-fixture-r2-linux-amd64" \
  --team-id t_eee8046ca49c271f5bea06acf285be66 \
  --project-id p_e5123bae12388454bb82fc1590750ab9 \
  --host b-cfaa2f2.preview.ecp.lingtong.com \
  --source-file "$source_file" \
  --source-sha256 4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3 \
  --built-file "$built_file" \
  --built-sha256 84185759f85cb3e90756642f0f1d63efef87cf2db3168306588bbb7774cac78b \
  --builder-ref operator-fixture:github-actions-37475046275-node22
