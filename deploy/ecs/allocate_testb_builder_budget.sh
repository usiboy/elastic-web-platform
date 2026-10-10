#!/usr/bin/env bash
# 仅给授权测试租户 B 预留试点 Builder 所需的逻辑上限；不表示物理配给。
# 要求独立新平台数据库事先备份，版本/旧数值不吻合就停止，不覆盖并发修改。
set -euo pipefail

root=/data/ewp-preview-20261006
private="$root/private"
test -s "$root/state/pre-testb-budget-builder.dump"
team=t_eee8046ca49c271f5bea06acf285be66
project=p_e5123bae12388454bb82fc1590750ab9

get() {
  local file="$1" path="$2" secret
  IFS= read -r secret < "$file"
  printf 'header = "Authorization: Bearer %s"\n' "$secret" | \
    curl --config - --noproxy '*' --silent --show-error --cacert "$private/ca.crt" \
      --resolve console.ecp.lingtong.com:443:127.0.0.1 \
      "https://console.ecp.lingtong.com/v1$path"
  secret=""
}

put() {
  local file="$1" path="$2" body="$3" secret
  IFS= read -r secret < "$file"
  printf 'header = "Authorization: Bearer %s"\n' "$secret" | \
    curl --config - --noproxy '*' --silent --show-error --cacert "$private/ca.crt" \
      --resolve console.ecp.lingtong.com:443:127.0.0.1 \
      --header 'Content-Type: application/json' --request PUT --data "$body" \
      --output /dev/null --write-out '%{http_code}' \
      "https://console.ecp.lingtong.com/v1$path"
  secret=""
}

get "$private/owner-b.token" "/teams/$team/resources" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["version"]==1 and d["budget"]=={"cpuMilli":1000,"memoryMiB":2048,"storageMiB":1024}'
get "$private/owner-b.token" "/teams/$team/projects/$project/resources" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["version"]==1 and d["resources"]=={"cpuMilli":500,"memoryMiB":512,"storageMiB":256}'
test "$(put "$private/bootstrap.token" "/admin/teams/$team/resource-budget" '{"expectedVersion":1,"cpuMilli":2500,"memoryMiB":4096,"storageMiB":4096}')" = 200
test "$(put "$private/owner-b.token" "/teams/$team/projects/$project/resources" '{"expectedVersion":1,"cpuMilli":2000,"memoryMiB":3072,"storageMiB":2048}')" = 200
get "$private/owner-b.token" "/teams/$team/resources" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["version"]==2 and d["budget"]=={"cpuMilli":2500,"memoryMiB":4096,"storageMiB":4096} and d["reserved"]=={"cpuMilli":2000,"memoryMiB":3072,"storageMiB":2048}'
get "$private/owner-b.token" "/teams/$team/projects/$project/resources" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["version"]==2 and d["resources"]=={"cpuMilli":2000,"memoryMiB":3072,"storageMiB":2048} and d["state"]=="reserved_not_provisioned"'
printf 'testB_builder_budget=2500m/4096MiB/4096MiB reservation=2000m/3072MiB/2048MiB state=reserved_not_provisioned\n'
