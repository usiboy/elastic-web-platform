#!/usr/bin/env bash
# 只向独立测试库的现有 A/B Team 登记实验性预算与项目预留；不启动容器/卷。
# 初次运行要求全库尚无任何预算或预留；只把私有令牌送给本机 HTTPS API。
set -euo pipefail

root=/data/ewp-preview-20261006
private="$root/private"
existing=$(docker exec ewp-postgres-preview-20261006 psql -U postgres -Atc 'SELECT (SELECT count(*) FROM team_resource_budgets)+(SELECT count(*) FROM project_resource_reservations)')
test "$existing" = 0

call() {
  local secret_file="$1" method="$2" path="$3" body="$4"
  local secret
  IFS= read -r secret < "$secret_file"
  printf 'header = "Authorization: Bearer %s"\n' "$secret" | \
    curl --config - --noproxy '*' --silent --show-error \
      --cacert "$private/ca.crt" --resolve console.ecp.lingtong.com:443:127.0.0.1 \
      --header 'Content-Type: application/json' --request "$method" --data "$body" \
      --output /dev/null --write-out '%{http_code}' \
      "https://console.ecp.lingtong.com/v1$path"
  secret=""
}

team_a=t_f9cf963718a72612c48940af8a8296e4
team_b=t_eee8046ca49c271f5bea06acf285be66
project_a=p_34401f5c05af1079010cb95ee3ec1275
project_b=p_e5123bae12388454bb82fc1590750ab9

test "$(call "$private/bootstrap.token" PUT "/admin/teams/$team_a/resource-budget" '{"expectedVersion":0,"cpuMilli":1500,"memoryMiB":4096,"storageMiB":4096}')" = 201
test "$(call "$private/bootstrap.token" PUT "/admin/teams/$team_b/resource-budget" '{"expectedVersion":0,"cpuMilli":1000,"memoryMiB":2048,"storageMiB":1024}')" = 201
test "$(call "$private/owner-a.token" PUT "/teams/$team_a/projects/$project_a/resources" '{"expectedVersion":0,"cpuMilli":1000,"memoryMiB":2048,"storageMiB":2048}')" = 201
test "$(call "$private/owner-b.token" PUT "/teams/$team_b/projects/$project_b/resources" '{"expectedVersion":0,"cpuMilli":500,"memoryMiB":512,"storageMiB":256}')" = 201

# A/B 的预算不能借用，已有预留不能因超额更新被改写。
test "$(call "$private/owner-b.token" PUT "/teams/$team_b/projects/$project_b/resources" '{"expectedVersion":1,"cpuMilli":1200,"memoryMiB":512,"storageMiB":256}')" = 409
printf 'test_resource_reservations teamA=budgeted teamB=budgeted projects=reserved_not_provisioned overBudget=409\n'
