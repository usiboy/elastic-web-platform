#!/usr/bin/env bash
# 本轮操作员可信 fixture：仅把已验证源码与新构建的 React 静态字节登记为私有候选。
# GitHub App 私钥不在 ECS；不变更老站点或开启任意租户代码执行。
set -euo pipefail

root=/data/ewp-preview-20261006
source_file="$root/state/fixture-inputs/repo-1404606859-9ea1fc0c7037.tar.gz"
built_file="$root/state/fixture-inputs/testB-built-no-treeshake-9ea1fc0c7037.tar.gz"

"$root/bin/ewp-operator-fixture-linux-amd64" \
  --team-id t_eee8046ca49c271f5bea06acf285be66 \
  --project-id p_e5123bae12388454bb82fc1590750ab9 \
  --host b.preview.ecp.lingtong.com \
  --source-file "$source_file" \
  --source-sha256 044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7e55ce7eaf5cd30d906a \
  --built-file "$built_file" \
  --built-sha256 92d909ef69e684f09fcdd82ed88468e7181ce74a2fec5c69547db1818effe712 \
  --builder-ref operator-fixture:node22-vite7-no-treeshake
