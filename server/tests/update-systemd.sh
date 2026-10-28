#!/usr/bin/env bash
set -euo pipefail
[[ $(id -u) == 0 ]] || { echo 'Run the isolated systemd test as root.' >&2; exit 1; }
systemctl is-system-running --wait || [[ $(systemctl is-system-running) == degraded ]]
MUSIC_SERVER_SYSTEMD_TEST=1 go test ./internal/update -run '^TestSystemd(Install|SchemaMigration|RealApplication)|^TestWorkerRefreshLinuxOwnershipAndLock$' -count=1 -timeout 240s -v
