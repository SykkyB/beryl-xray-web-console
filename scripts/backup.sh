#!/usr/bin/env bash
# Pull a self-contained backup of everything we deployed on the router:
# binaries, init scripts, UCI configs, hotplug hooks, panel config and
# saved VLESS profiles, sysctl tweaks. Output is a tar.gz under
# backups/, named with the UTC timestamp; the directory is gitignored
# because the contents include secrets (VLESS UUID, bcrypt hash).
#
# Usage:
#   scripts/backup.sh [ssh-target]      # default: beryl
#
# Restore (from a fresh OpenWrt with same arch):
#   scp -O backups/beryl-YYYYMMDD-HHMMSSZ.tar.gz beryl:/tmp/
#   ssh beryl 'tar xzf /tmp/beryl-...tar.gz -C /'
#   ssh beryl '/etc/init.d/sing-box enable; /etc/init.d/sing-box start'
#   ssh beryl '/etc/init.d/xray-panel-cli enable; /etc/init.d/xray-panel-cli start'

set -euo pipefail

TARGET="${1:-beryl}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST_DIR="$ROOT/backups"
mkdir -p "$DEST_DIR"

TS="$(date -u +%Y%m%d-%H%M%SZ)"
DEST="$DEST_DIR/beryl-$TS.tar.gz"

# Paths to capture. Trailing slash on directories means "include all
# contents". Ordered roughly: binaries, sing-box runtime, init/hotplug,
# panel runtime, sysctl.
PATHS=(
    /usr/bin/sing-box
    /usr/bin/xray-panel-cli
    /etc/sing-box
    /etc/config/sing-box
    # GL.iNet's physical-switch binding — Side switch (Phase 4) writes
    # @main[0].func='xray' here. Restoring this preserves whether the
    # switch was claimed by us at backup time; otherwise a restore would
    # leave bind_switch=1 in sing-box but func='vpn' here → inconsistent.
    /etc/config/switch-button
    /etc/init.d/sing-box
    /etc/init.d/xray-panel-cli
    /etc/hotplug.d/button/50-sing-box-switch
    /etc/xray-panel-cli
    /etc/sysctl.d/99-disable-mptcp.conf
    # GL.iNet UI launcher integration
    /www/xray-panel-launcher.js
    /www/gl_home.html
    /www/gl_home.html.bak
)

GIT_REV="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
GIT_DIRTY="$(git -C "$ROOT" status --porcelain 2>/dev/null | wc -l | tr -d ' ')"
GIT_BRANCH="$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)"

echo ">>> Backing up from '$TARGET' → $DEST"
echo ">>> Repo git: $GIT_BRANCH @ $GIT_REV (dirty files: $GIT_DIRTY)"

# Collect remote info one piece at a time so a missing helper (e.g.
# busybox without hostname) doesn't kill the whole pipe.
collect() {
    ssh "$TARGET" "$1" 2>/dev/null || echo "?"
}

R_HOST="$(collect 'cat /proc/sys/kernel/hostname')"
R_UPTIME="$(collect 'uptime')"
R_OPENWRT="$(collect 'sed -n s/.*DESCRIPTION=//p /etc/openwrt_release | tr -d \\\"')"
R_SINGBOX="$(collect '/usr/bin/sing-box version 2>/dev/null | head -1')"
R_KERNEL="$(collect 'uname -srm')"
R_UCI_SINGBOX="$(collect 'uci show sing-box')"

# Build local manifest, then ship it to the router so it can be added
# to the same tar stream alongside the real files.
LOCAL_MANIFEST="$(mktemp)"
trap 'rm -f "$LOCAL_MANIFEST"' EXIT
cat >"$LOCAL_MANIFEST" <<EOF
=== beryl-xray-web-console backup manifest ===

backup_utc: $TS
git_branch: $GIT_BRANCH
git_revision: $GIT_REV
git_dirty_files: $GIT_DIRTY

--- router ---
host: $R_HOST
uptime: $R_UPTIME
openwrt: $R_OPENWRT
kernel: $R_KERNEL

--- sing-box ---
$R_SINGBOX

--- uci sing-box ---
$R_UCI_SINGBOX

--- captured paths ---
$(printf '  %s\n' "${PATHS[@]}")
EOF

# Push manifest to router so it ends up inside the tarball at a
# predictable path. Cleaned up after the tar pipe.
scp -O -q "$LOCAL_MANIFEST" "$TARGET:/tmp/backup-manifest.txt"

# busybox tar doesn't support --ignore-failed-read, so we pre-filter
# the path list on the router and feed only the existing entries via
# stdin (-T -). The manifest is already on the router from the scp
# above; prepend it.
PATHS_STR="${PATHS[*]}"
ssh "$TARGET" "
    {
        echo /tmp/backup-manifest.txt
        for p in $PATHS_STR; do
            [ -e \"\$p\" ] && echo \"\$p\"
        done
    } | tar czf - -T -
" > "$DEST"

ssh "$TARGET" 'rm -f /tmp/backup-manifest.txt' || true

SIZE="$(ls -lh "$DEST" | awk '{print $5}')"
COUNT="$(tar tzf "$DEST" | wc -l | tr -d ' ')"

echo ">>> Wrote $DEST ($SIZE, $COUNT entries)"
echo
echo "--- Manifest ---"
tar xzf "$DEST" -O tmp/backup-manifest.txt 2>/dev/null
echo
echo "--- First entries ---"
tar tzf "$DEST" | head -25

# Off-site mirror to Cloudflare R2.
# Uses the rclone profile `r2` from ~/.config/rclone/rclone.conf.
# If rclone or the profile is missing, skip silently with a notice — the
# local snapshot is still on disk.
R2_REMOTE="r2:sys-lab-home-backups/beryl-snapshots"
if command -v rclone >/dev/null 2>&1 && rclone listremotes 2>/dev/null | grep -q '^r2:'; then
    echo
    echo ">>> Uploading to $R2_REMOTE/"
    if rclone copy --s3-no-head "$DEST" "$R2_REMOTE/"; then
        echo ">>> R2 upload OK"
    else
        echo ">>> R2 upload FAILED — local snapshot still at $DEST" >&2
    fi
else
    echo
    echo ">>> rclone or [r2] profile not found, skipping R2 mirror"
    echo ">>> (install: brew install rclone; configure profile per ~/.r2-creds.env)"
fi
