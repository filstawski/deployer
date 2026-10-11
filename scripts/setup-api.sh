#!/bin/sh
# Expose the authenticated API through existing nginx and wildcard TLS.
set -eu
umask 077
domain=${1:-}
port=${2:-7420}
fail() { printf '%s\n' "deployer API setup: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'run as root on the server'
case "$domain" in ''|*[!a-z0-9.-]*|.*|-*) fail 'usage: sh setup-api.sh BASE_DOMAIN [API_PORT]';; esac
case "$domain" in *.*) ;; *) fail 'provide a full domain name';; esac
case "$port" in ''|*[!0-9]*) fail 'invalid API port';; esac
[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail 'invalid API port'
command -v nginx >/dev/null || fail 'nginx is required'
command -v deployer >/dev/null || fail 'install Deployer first'
cert="/etc/letsencrypt/live/$domain/fullchain.pem"
key="/etc/letsencrypt/live/$domain/privkey.pem"
[ -r "$cert" ] && [ -r "$key" ] || fail 'wildcard certificate files are missing'
[ -d /etc/nginx/conf.d ] || fail 'nginx conf.d directory is missing'
nginx -t
deployer --server "http://127.0.0.1:$port" --token-file /var/lib/deployer/operator.token server status --json
target=/etc/nginx/conf.d/deployer-api.conf
staged=$(mktemp /etc/nginx/conf.d/.deployer-api.XXXXXXXX)
dump=$(mktemp /tmp/deployer-nginx.XXXXXXXX)
created=false
complete=false
cleanup() {
  rm -f "$staged" "$dump"
  if [ "$created" = true ] && [ "$complete" = false ]; then rm -f "$target"; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cat > "$staged" <<EOF
# Managed by Deployer API setup. API authentication is enforced by deployerd.
limit_req_zone \$binary_remote_addr zone=deployer_api_requests:10m rate=10r/s;
server {
    listen 80;
    server_name api.$domain;
    return 308 https://api.$domain\$request_uri;
}
server {
    listen 443 ssl;
    server_name api.$domain;
    ssl_certificate $cert;
    ssl_certificate_key $key;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    client_max_body_size 64k;
    location /v1/ {
        limit_req zone=deployer_api_requests burst=20 nodelay;
        limit_req_status 429;
        proxy_pass http://127.0.0.1:$port;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header Connection "";
        proxy_intercept_errors off;
        proxy_read_timeout 30s;
    }
    location = / {
        default_type text/plain;
        return 200 "Deployer management API. Connect with the Deployer CLI.\\n";
    }
    location / { return 404; }
}
EOF
[ ! -L "$target" ] || fail 'refusing to replace a symlink at deployer-api.conf'
if [ -e "$target" ]; then
  cmp -s "$staged" "$target" || fail 'deployer-api.conf already exists with different content; left untouched'
else
  # The file is complete before nginx can load it.
  chmod 644 "$staged"
  mv "$staged" "$target"
  created=true
fi
nginx -t
nginx -T > "$dump" 2>/dev/null
grep -Fq "# configuration file $target:" "$dump" || fail 'nginx does not include the new API configuration'
systemctl reload nginx
complete=true
printf 'API available at https://api.%s (management requests require a token).\n' "$domain"
