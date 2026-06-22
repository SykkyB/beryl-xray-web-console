#!/usr/bin/env bash
# Pull a self-contained backup of everything we deployed on the router:
# binaries, init scripts, UCI configs, hotplug hooks, panel config and
# saved VLESS profiles, sysctl tweaks. Output is a tar.gz under
# backups/, named with the UTC timestamp; the directory is gitignored
# because the contents include secrets (VLESS UUID, bcrypt hash).
#
# Scope widened 2026-06-22 (after the MT6000 4.9.0 reflash incident) to the
# whole router config (/etc/config, AdGuard, /etc/xray, dropbear, crontab, ...),
# so a clean flash is fully recoverable. Local snapshot stays plaintext under
# backups/ (gitignored); the R2 mirror is AES-256 encrypted (secrets inside).
# The router also self-backs-up the same scope weekly via /usr/sbin/beryl-config-backup.
#
# Usage:
#   scripts/backup.sh [ssh-target]      # default: beryl
#
# Restore (from a fresh OpenWrt, same arch — decrypt the R2 .enc first if
# that's your source: openssl enc -d -aes-256-cbc -pbkdf2 -pass file:<pass> ...):
#   scp -O backups/beryl-YYYYMMDD-HHMMSSZ.tar.gz beryl:/tmp/
#   ssh beryl 'tar xzf /tmp/beryl-...tar.gz -C /'     # restores /etc/config etc.
#   ssh beryl 'reboot'                                # apply UCI
#   # then re-enable our services:
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
    # --- full router config (added 2026-06-22 after the MT6000 4.9.0 reflash
    # incident: a clean firmware flash wipes everything not explicitly kept,
    # so back up enough to fully restore — network/wifi/wireguard/firewall/
    # adguard/xray/ssh/cron — not just our deployment). /etc/config includes
    # sing-box and switch-button, so those are no longer listed separately.
    /etc/config
    /etc/AdGuardHome/config.yaml   # custom DNS rewrites/clients (out of /etc/config)
    /etc/xray                      # xray config.json
    /etc/dropbear                  # ssh host keys + authorized_keys
    /etc/passwd
    /etc/shadow
    /etc/group
    /etc/crontabs
    /etc/rclone                    # R2 creds (needed to re-run backups)
    /etc/openvpn
    /etc/wireguard                 # AmneziaWG obfuscation params (if enabled)
    /root
    # --- our deployment ---
    /usr/bin/sing-box
    /usr/bin/xray-panel-cli
    /etc/sing-box
    /etc/init.d/sing-box
    /etc/init.d/xray-panel-cli
    /etc/hotplug.d/button/50-sing-box-switch
    /etc/xray-panel-cli
    # /etc/xray-panel-cli contains panel.yaml, profiles.json, sources.json
    # (VPN Scout) and the scans/ subdir of snapshot JSONs — all swept up
    # by including the dir.
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

# Off-site mirror to Cloudflare R2 — ENCRYPTED.
# The local snapshot under backups/ stays plaintext (gitignored, on your Mac),
# but the R2 copy is AES-256 encrypted because it now carries secrets (WG
# private keys, ssh host keys, /etc/shadow). Pass is pulled from the router
# (/etc/beryl-backup.pass == flint2 /etc/system-backup.pass, dup in 1Password).
# Fail-closed: if the pass is missing or encryption fails, we do NOT upload.
R2_REMOTE="r2:sys-lab-home-backups/beryl-snapshots"
ENC="$DEST.enc"
PASS_TMP="$(mktemp)"
trap 'rm -f "$LOCAL_MANIFEST" "$PASS_TMP" "$ENC"' EXIT
if command -v rclone >/dev/null 2>&1 && rclone listremotes 2>/dev/null | grep -q '^r2:'; then
    echo
    if ! ssh "$TARGET" 'cat /etc/beryl-backup.pass' > "$PASS_TMP" 2>/dev/null || [ ! -s "$PASS_TMP" ]; then
        echo ">>> no /etc/beryl-backup.pass on $TARGET — skipping R2 (won't upload plaintext secrets)" >&2
    elif ! openssl enc -aes-256-cbc -pbkdf2 -salt -pass "file:$PASS_TMP" -in "$DEST" -out "$ENC"; then
        echo ">>> encryption failed — skipping R2 (won't upload plaintext secrets)" >&2
    else
        echo ">>> Uploading encrypted snapshot to $R2_REMOTE/"
        if rclone copy --s3-no-head "$ENC" "$R2_REMOTE/"; then
            echo ">>> R2 upload OK ($(basename "$ENC"))"
        else
            echo ">>> R2 upload FAILED — local snapshot still at $DEST" >&2
        fi
    fi
else
    echo
    echo ">>> rclone or [r2] profile not found, skipping R2 mirror"
    echo ">>> (install: brew install rclone; configure profile per ~/.r2-creds.env)"
fi
