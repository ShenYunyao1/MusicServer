#!/usr/bin/env bash
# Install as /etc/letsencrypt/renewal-hooks/deploy/musicserver-nginx (root, 0755).
set -euo pipefail
/usr/sbin/nginx -t
/bin/systemctl reload nginx.service
