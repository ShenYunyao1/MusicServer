#!/usr/bin/env bash
# Bootstrap only. Subsequent web upgrades use the independent, signed-package installer.
set -euo pipefail
if [[ $# -gt 1 || (${1:-} != '' && ${1:-} != --prepare-only) ]]; then echo 'Usage: install.sh [--prepare-only]' >&2; exit 1; fi
prepare_only=${1:-}
if [[ $EUID != 0 ]]; then echo 'Run with sudo from an extracted, verified release directory.' >&2; exit 1; fi
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then echo 'This release supports Ubuntu Linux amd64.' >&2; exit 1; fi
cd -- "$(dirname -- "$(readlink -f -- "$0")")/.."
for required in musicserver musicserver-updater build.json; do [[ -f "$required" && ! -L "$required" ]] || { echo "Missing $required" >&2; exit 1; }; done
if [[ -e /opt/musicserver/current || -L /opt/musicserver/current ]]; then echo 'Already installed. Use the web updater; this bootstrap never replaces existing data.' >&2; exit 1; fi
command -v ffprobe >/dev/null || { echo 'Install ffmpeg first: sudo apt-get update && sudo apt-get install -y ffmpeg' >&2; exit 1; }
id musicserver >/dev/null 2>&1 || useradd --system --home-dir /var/lib/musicserver --shell /usr/sbin/nologin musicserver
install -d -m 0755 /opt/musicserver /opt/musicserver/releases /etc/musicserver /usr/local/lib/musicserver /var/lib/musicserver-update
install -d -o musicserver -g musicserver -m 0700 /var/lib/musicserver
install -d -o musicserver -g musicserver -m 0755 /srv/musicserver/media
# The application may submit requests, but cannot edit updater state or installed releases.
install -d -o root -g musicserver -m 2770 /var/lib/musicserver-update/queue
install -d -o root -g root -m 0700 /var/lib/musicserver-update/backups /var/lib/musicserver-update/downloads
install -d -m 0755 /opt/musicserver/releases/initial
install -m 0755 musicserver /opt/musicserver/releases/initial/musicserver
install -m 0644 build.json /opt/musicserver/releases/initial/build.json
install -m 0755 musicserver-updater /usr/local/lib/musicserver/musicserver-updater
ln -s releases/initial /opt/musicserver/current
[[ -f /etc/musicserver/musicserver.env ]] || install -o root -g musicserver -m 0640 deploy/musicserver.env.example /etc/musicserver/musicserver.env
[[ -f /etc/musicserver/updater.json ]] || install -m 0644 deploy/updater.json.example /etc/musicserver/updater.json
install -m 0644 deploy/musicserver.service deploy/musicserver-update.service deploy/musicserver-update.path deploy/musicserver-update-recovery.timer /etc/systemd/system/
systemctl daemon-reload
if [[ $prepare_only == --prepare-only ]]; then
    echo 'Preparation complete. Start the services, then initialize the administrator in your browser:'
    echo 'systemctl enable --now musicserver.service musicserver-update.path musicserver-update-recovery.timer'
    exit 0
fi
systemctl enable --now musicserver.service musicserver-update.path musicserver-update-recovery.timer
echo 'Installation complete. Open the web page through SSH forwarding to set the administrator password.'
echo 'Initialization authorization is stored in /var/lib/musicserver/setup-token. Do not share it.'
echo 'Configure HTTPS and MUSIC_SERVER_PUBLIC_URL before public access.'
