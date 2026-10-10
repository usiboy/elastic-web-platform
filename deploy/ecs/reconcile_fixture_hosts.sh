#!/usr/bin/env bash
# 只在新项目自己的数据库执行迁移 014，并幂等读回 A/B 可信候选。
# 不读取 GitHub 私钥，不停旧服务，不改变固定 Host 指向。
set -euo pipefail

root=/data/ewp-preview-20261006
tool="$root/bin/ewp-operator-fixture-r2-linux-amd64"
input="$root/state/fixture-inputs"

"$tool" \
  --team-id t_f9cf963718a72612c48940af8a8296e4 \
  --project-id p_34401f5c05af1079010cb95ee3ec1275 \
  --host a.preview.ecp.lingtong.com \
  --source-file "$input/repo-1404606559-b7dc40fd3655.tar.gz" \
  --source-sha256 06eb9696ea468930721f5b4f3eff2d90819ef3553385ca2a0a342b4585c0eaa7 \
  --built-file "$input/testA-built-b7dc40fd3655.tar.gz" \
  --built-sha256 95b606560d3d47f90304dde1e830fe4e102bac5ebc022a3dd971a06ce0461dfd \
  --builder-ref operator-fixture:node22-vite7

"$tool" \
  --team-id t_eee8046ca49c271f5bea06acf285be66 \
  --project-id p_e5123bae12388454bb82fc1590750ab9 \
  --host b.preview.ecp.lingtong.com \
  --source-file "$input/repo-1404606859-9ea1fc0c7037.tar.gz" \
  --source-sha256 044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7e55ce7eaf5cd30d906a \
  --built-file "$input/testB-built-no-treeshake-9ea1fc0c7037.tar.gz" \
  --built-sha256 92d909ef69e684f09fcdd82ed88468e7181ce74a2fec5c69547db1818effe712 \
  --builder-ref operator-fixture:node22-vite7-no-treeshake
