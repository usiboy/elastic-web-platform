#!/usr/bin/env bash
# 仅用于指定测试租户：导入已与固定 Git 提交和成功 GitHub Actions 制品逐字节核对的静态模板。
# 这是操作员声明，不是自动 Builder 签名或多租户代码准入。
set -euo pipefail

root=/data/ewp-preview-20261006
"$root/bin/ewp-operator-fixture-r2-linux-amd64" \
  --team-id t_f9cf963718a72612c48940af8a8296e4 \
  --project-id p_34401f5c05af1079010cb95ee3ec1275 \
  --host a-e6062c7.preview.ecp.lingtong.com \
  --source-file "$root/state/fixture-inputs/repo-1404606559-e6062c7469d1.tar.gz" \
  --source-sha256 e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d \
  --built-file "$root/state/fixture-inputs/testA-ci-e6062c7.tar.gz" \
  --built-sha256 dc93eddd2391fdc06e2511f47eb35b3db69ade4c5c0c6deacf26ef662fe6c2a3 \
  --builder-ref operator-fixture:github-actions-37422492357-node22
