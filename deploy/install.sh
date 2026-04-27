#!/usr/bin/env bash
# Cross-compile xray-panel-cli and install it on a GL.iNet Beryl router via SSH.
#
# Usage:   deploy/install.sh beryl
#          (expects an ssh alias in ~/.ssh/config, or use root@host)
#
# Idempotent: copies the binary and init script every time, but writes
# /etc/xray-panel-cli/panel.yaml from the example only on first install
# so credentials survive reinstalls.

set -euo pipefail

if [ $# -lt 1 ]; then
	echo "usage: $0 <ssh-target>   (e.g. beryl or root@192.168.200.1)" >&2
	exit 2
fi

TARGET="$1"
SSH_OPTS="${SSH_OPTS:--o StrictHostKeyChecking=accept-new -o ConnectTimeout=5}"
# -O forces legacy scp1: OpenWrt's dropbear has no sftp-server, and
# macOS's OpenSSH 9+ picks sftp by default.
SCP_OPTS="${SCP_OPTS:--O}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$(mktemp -d)"
BIN="$BUILD_DIR/xray-panel-cli"

cleanup() { rm -rf "$BUILD_DIR"; }
trap cleanup EXIT

echo ">>> building xray-panel-cli for linux/arm64"
(
	cd "$REPO_ROOT"
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -trimpath -ldflags='-s -w' -o "$BIN" ./cmd/xray-panel-cli
)
ls -la "$BIN"

echo ">>> copying artifacts to $TARGET"
# shellcheck disable=SC2086
scp $SSH_OPTS $SCP_OPTS \
	"$BIN" \
	"$REPO_ROOT/deploy/xray-panel-cli.init" \
	"$REPO_ROOT/deploy/panel.example.yaml" \
	"$REPO_ROOT/router/etc/init.d/sing-box" \
	"$REPO_ROOT/router/etc/hotplug.d/button/50-sing-box-switch" \
	"$REPO_ROOT/router/www/xray-panel-launcher.js" \
	"$TARGET:/tmp/"

echo ">>> installing on $TARGET"
# shellcheck disable=SC2087,SC2086
ssh $SSH_OPTS "$TARGET" /bin/sh <<'REMOTE'
set -eu

# Disable kernel-level Multipath TCP. The MPTCP implementation in this
# OpenWrt kernel build is incomplete (subflow_v4_init_req is a stub);
# any TCP listener that doesn't explicitly opt out — Go's net/http does
# not — receives mangled SYN-ACKs that route via lo with cleared headers
# and the LAN client never gets a reply. uhttpd works because GL.iNet
# patches it to disable MPTCP on its sockets. Doing it system-wide is
# simpler and harmless: nothing on this firmware actually uses MPTCP.
mkdir -p /etc/sysctl.d
echo 'net.mptcp.enabled=0' > /etc/sysctl.d/99-disable-mptcp.conf
sysctl -w net.mptcp.enabled=0 >/dev/null 2>&1 || true

mkdir -p /etc/xray-panel-cli

# OpenWrt's busybox lacks coreutils' install, so cp + chmod by hand.
cp /tmp/xray-panel-cli      /usr/bin/xray-panel-cli
chmod 0755                  /usr/bin/xray-panel-cli
cp /tmp/xray-panel-cli.init /etc/init.d/xray-panel-cli
chmod 0755                  /etc/init.d/xray-panel-cli

# Refresh the sing-box init + hotplug bits the panel relies on.
mkdir -p /etc/hotplug.d/button
cp /tmp/sing-box                  /etc/init.d/sing-box
chmod 0755                        /etc/init.d/sing-box
cp /tmp/50-sing-box-switch        /etc/hotplug.d/button/50-sing-box-switch
chmod 0755                        /etc/hotplug.d/button/50-sing-box-switch

# Floating-button launcher injected into the GL.iNet stock admin UI
# (/www/gl_home.html). One-time backup of the original; idempotent
# re-patch on every install — looks for the marker before inserting.
# Firmware updates that rewrite gl_home.html will undo the patch;
# re-running this installer puts it back.
cp /tmp/xray-panel-launcher.js    /www/xray-panel-launcher.js
chmod 0644                        /www/xray-panel-launcher.js
if [ -f /www/gl_home.html ]; then
	[ -f /www/gl_home.html.bak ] || cp /www/gl_home.html /www/gl_home.html.bak
	# Cache-bust the script tag with a content-derived query-string so
	# the browser pulls each new launcher.js without needing a manual
	# Cmd+Shift+R. Without this, GL.iNet's nginx serves the script with
	# heuristic caching and a stale version sticks until the user
	# explicitly invalidates — invisible to anyone who doesn't know the
	# integration is there. md5sum is in busybox; first 10 hex chars
	# is more than enough collision-wise for "did the file change".
	LAUNCHER_HASH="$(md5sum /www/xray-panel-launcher.js | cut -c1-10)"
	if ! grep -q xray-panel-launcher /www/gl_home.html; then
		# First insertion: drop the tag with the hash query right
		# before </body>. busybox sed doesn't grok newlines in the
		# replacement, so the tag goes inline on the same line.
		sed -i "s|</body>|<script src=\"/xray-panel-launcher.js?v=${LAUNCHER_HASH}\" defer></script></body>|" \
			/www/gl_home.html
	else
		# Re-patch: replace whatever existing src=... value (with or
		# without ?v=…) by the new hashed one, so subsequent installs
		# always advance the cache key.
		sed -i "s|src=\"/xray-panel-launcher.js[^\"]*\"|src=\"/xray-panel-launcher.js?v=${LAUNCHER_HASH}\"|" \
			/www/gl_home.html
	fi
fi

if [ ! -f /etc/xray-panel-cli/panel.yaml ]; then
	cp /tmp/panel.example.yaml /etc/xray-panel-cli/panel.yaml
	chmod 0600                 /etc/xray-panel-cli/panel.yaml
	echo
	echo "==> /etc/xray-panel-cli/panel.yaml created from example."
	echo "==> Edit it (set bcrypt password) before starting:"
	echo "==>   ssh $(hostname) vi /etc/xray-panel-cli/panel.yaml"
	echo "==>   ssh $(hostname) /etc/init.d/xray-panel-cli enable"
	echo "==>   ssh $(hostname) /etc/init.d/xray-panel-cli start"
fi

# Clean up tmp drops.
rm -f /tmp/xray-panel-cli /tmp/xray-panel-cli.init /tmp/panel.example.yaml \
      /tmp/sing-box /tmp/50-sing-box-switch /tmp/xray-panel-launcher.js

# If the service is already enabled+running, restart it to pick up the
# new binary. Don't auto-start the first install: panel.yaml still has
# a placeholder bcrypt that would refuse to validate.
if [ -L /etc/rc.d/S95xray-panel-cli ]; then
	/etc/init.d/xray-panel-cli restart
	echo "==> restarted"
fi
REMOTE

echo ">>> done"
