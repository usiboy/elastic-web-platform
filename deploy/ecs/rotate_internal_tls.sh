#!/usr/bin/env bash
# 仅轮换新平台回环 Gateway/API 和新平台测试 Host 使用的内部 CA/叶证书。
# 原 IP 站点的公网证书、旧服务和旧数据库不在此脚本范围内。
set -euo pipefail

private=/data/ewp-preview-20261006/private
ca_next="$private/internal-ca-20261006.crt"
cert_next="$private/internal-site-20261006.crt"
key_next="$private/internal-site-20261006.key"
for current in ca.crt server.crt server.key; do
  test -f "$private/$current"
  test ! -e "$private/$current.pre-20261006-r2"
done
openssl verify -CAfile "$ca_next" -verify_hostname p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com "$cert_next" >/dev/null
cert_pub=$(openssl x509 -in "$cert_next" -pubkey -noout | openssl dgst -sha256)
key_pub=$(openssl pkey -in "$key_next" -pubout | openssl dgst -sha256)
test "$cert_pub" = "$key_pub"

for current in ca.crt server.crt server.key; do
  cp -p "$private/$current" "$private/$current.pre-20261006-r2"
done

committed=false
restore() {
  if [ "$committed" = false ]; then
    for current in ca.crt server.crt server.key; do
      cp -p "$private/$current.pre-20261006-r2" "$private/$current"
    done
    systemctl restart ewp-preview-api-20261006.service ewp-preview-site-20261006.service
    nginx -t && nginx -s reload
  fi
}
trap restore EXIT

install -m 0600 "$ca_next" "$private/ca.crt"
install -m 0600 "$cert_next" "$private/server.crt"
install -m 0600 "$key_next" "$private/server.key"
nginx -t
systemctl restart ewp-preview-api-20261006.service ewp-preview-site-20261006.service
nginx -s reload

# Nginx reload 切换 worker 是异步的；等真实新 CA/API 和公网 HTTP 都可用再提交。
deadline=$((SECONDS + 15))
control_code=unavailable
public_code=unavailable
while (( SECONDS < deadline )); do
  control_code=$(curl --noproxy '*' --silent --cacert "$ca_next" --resolve console.ecp.lingtong.com:443:127.0.0.1 -o /dev/null -w '%{http_code}' https://console.ecp.lingtong.com/v1/me) || control_code=unavailable
  public_code=$(curl --noproxy '*' --silent --resolve p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com:80:127.0.0.1 -o /dev/null -w '%{http_code}' http://p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com/) || public_code=unavailable
  if [ "$control_code" = 401 ] && [ "$public_code" = 200 ]; then
    break
  fi
  sleep 0.2
done
printf 'internal_tls_readback control=%s public_http=%s\n' "$control_code" "$public_code"
test "$control_code" = 401
test "$public_code" = 200
committed=true
printf 'internal_tls_rotated control=%s public_http=%s\n' "$control_code" "$public_code"
