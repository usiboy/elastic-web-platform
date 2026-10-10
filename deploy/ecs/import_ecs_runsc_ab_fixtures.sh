#!/usr/bin/env bash
# 原 ECS 同机独立 Docker daemon 的 runsc 构建字节，仅导入授权 A/B 私有 Preview。
# 操作员 fixture 路径，不宣称后台自动领取 Git 作业或生产 Builder 私钥签名。
set -euo pipefail

root=/data/ewp-preview-20261006
input="$root/state/fixture-inputs"
test -s "$root/state/pre-ecs-runsc-fixtures.dump"
test "$(sha256sum "$input/repo-1404606559-e6062c7469d1.tar.gz" | cut -d' ' -f1)" = e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d
test "$(sha256sum "$input/repo-1404606859-cfaa2f283f5c.tar.gz" | cut -d' ' -f1)" = 4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3
test "$(sha256sum "$input/testA-ecs-runsc-e6062c7.tar.gz" | cut -d' ' -f1)" = fcf4f4f5b3f08c46907b0d6269ac9044c94114b80502703e44d7deab6e1d563f
test "$(sha256sum "$input/testB-ecs-runsc-cfaa2f2.tar.gz" | cut -d' ' -f1)" = a77d2355de350ee73ba99f555bf74b8ff19314a10802d03281b06ac49a557d2f

"$root/bin/ewp-operator-fixture-r2-linux-amd64" \
  --team-id t_f9cf963718a72612c48940af8a8296e4 \
  --project-id p_34401f5c05af1079010cb95ee3ec1275 \
  --host a-ecsrunsc.preview.ecp.lingtong.com \
  --source-file "$input/repo-1404606559-e6062c7469d1.tar.gz" \
  --source-sha256 e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d \
  --built-file "$input/testA-ecs-runsc-e6062c7.tar.gz" \
  --built-sha256 fcf4f4f5b3f08c46907b0d6269ac9044c94114b80502703e44d7deab6e1d563f \
  --builder-ref operator-fixture:ecs-runsc-node22-20261007-A

"$root/bin/ewp-operator-fixture-r2-linux-amd64" \
  --team-id t_eee8046ca49c271f5bea06acf285be66 \
  --project-id p_e5123bae12388454bb82fc1590750ab9 \
  --host b-ecsrunsc.preview.ecp.lingtong.com \
  --source-file "$input/repo-1404606859-cfaa2f283f5c.tar.gz" \
  --source-sha256 4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3 \
  --built-file "$input/testB-ecs-runsc-cfaa2f2.tar.gz" \
  --built-sha256 a77d2355de350ee73ba99f555bf74b8ff19314a10802d03281b06ac49a557d2f \
  --builder-ref operator-fixture:ecs-runsc-node22-20261007-B
