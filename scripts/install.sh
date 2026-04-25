#!/usr/bin/env bash
#
# Push sing-box and all support files to the GL.iNet Beryl router via SSH.
# Assumes you have an SSH host alias `beryl` (or set HOST=...) that lands you
# as root on the router.
#
# What it does:
#   1. (optional) build a static sing-box binary if missing
#   2. scp binary into /usr/bin/sing-box
#   3. scp init script, UCI config, hotplug hook
#   4. scp sing-box runtime config (router/etc/sing-box/config.json — your
#      uncommitted local copy with real VLESS credentials)
#   5. enable + (re)start the service on the router
#
# Pre-flight on the router (one-time, you must do these manually):
#   opkg update
#   opkg install kmod-tun iptables-mod-tproxy   # likely already present
#   mkdir -p /etc/hotplug.d/button              # mt3000 stock has it empty
#
# Note: only file paths under router/etc/ are mirrored. The script does NOT
# overwrite anything outside that tree.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST="${HOST:-beryl}"
SSH_OPTS=(-o ConnectTimeout=5)
SCP_OPTS=(-O -o ConnectTimeout=5)   # -O = legacy SCP (busybox-friendly)

BIN="$ROOT/build/sing-box-build/sing-box-static"
LOCAL_CONFIG="$ROOT/router/etc/sing-box/config.json"

if [ ! -x "$BIN" ]; then
    echo ">>> binary not found, building"
    "$ROOT/scripts/build-sing-box.sh"
fi

if [ ! -f "$LOCAL_CONFIG" ]; then
    cat <<EOF >&2

ERROR: $LOCAL_CONFIG not found.

Copy router/etc/sing-box/config.example.json to config.json and fill in:
  __VPN_HOSTNAME__
  __VLESS_UUID__
  __REALITY_SNI__
  __REALITY_PUBLIC_KEY__
  __REALITY_SHORT_ID__

config.json is git-ignored on purpose (contains your VLESS auth UUID).
EOF
    exit 1
fi

echo ">>> ensuring hotplug.d/button exists on $HOST"
ssh "${SSH_OPTS[@]}" "$HOST" 'mkdir -p /etc/hotplug.d/button /etc/sing-box'

echo ">>> uploading binary"
scp "${SCP_OPTS[@]}" "$BIN" "$HOST:/tmp/sing-box.new"
ssh "${SSH_OPTS[@]}" "$HOST" 'chmod 0755 /tmp/sing-box.new && mv /tmp/sing-box.new /usr/bin/sing-box'

echo ">>> uploading config files"
scp "${SCP_OPTS[@]}" "$LOCAL_CONFIG"                                   "$HOST:/etc/sing-box/config.json"
scp "${SCP_OPTS[@]}" "$ROOT/router/etc/config/sing-box"                "$HOST:/etc/config/sing-box"
scp "${SCP_OPTS[@]}" "$ROOT/router/etc/init.d/sing-box"                "$HOST:/etc/init.d/sing-box"
scp "${SCP_OPTS[@]}" "$ROOT/router/etc/hotplug.d/button/50-sing-box-switch" \
                                                                       "$HOST:/etc/hotplug.d/button/50-sing-box-switch"

echo ">>> fixing perms"
ssh "${SSH_OPTS[@]}" "$HOST" '
    chmod 0755 /etc/init.d/sing-box /etc/hotplug.d/button/50-sing-box-switch &&
    chmod 0600 /etc/sing-box/config.json
'

echo ">>> validating config"
ssh "${SSH_OPTS[@]}" "$HOST" '/usr/bin/sing-box check -c /etc/sing-box/config.json && echo OK'

echo ">>> enabling + restarting service"
ssh "${SSH_OPTS[@]}" "$HOST" '/etc/init.d/sing-box enable; /etc/init.d/sing-box restart'

echo ">>> done"
