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
	"$TARGET:/tmp/"

echo ">>> installing on $TARGET"
# shellcheck disable=SC2087,SC2086
ssh $SSH_OPTS "$TARGET" /bin/sh <<'REMOTE'
set -eu

# OpenWrt's busybox lacks coreutils' install, so cp + chmod by hand.
cp /tmp/xray-panel-cli      /usr/bin/xray-panel-cli
chmod 0755                  /usr/bin/xray-panel-cli
cp /tmp/xray-panel-cli.init /etc/init.d/xray-panel-cli
chmod 0755                  /etc/init.d/xray-panel-cli

mkdir -p /etc/xray-panel-cli
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
rm -f /tmp/xray-panel-cli /tmp/xray-panel-cli.init /tmp/panel.example.yaml

# If the service is already enabled+running, restart it to pick up the
# new binary. Don't auto-start the first install: panel.yaml still has
# a placeholder bcrypt that would refuse to validate.
if [ -L /etc/rc.d/S95xray-panel-cli ]; then
	/etc/init.d/xray-panel-cli restart
	echo "==> restarted"
fi
REMOTE

echo ">>> done"
