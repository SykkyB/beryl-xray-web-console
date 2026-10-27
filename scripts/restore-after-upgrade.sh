#!/usr/bin/env bash
# restore-after-upgrade.sh — восстановление кастома Beryl AX (GL-MT3000) после
# sysupgrade GL.iNet (Keep Settings) или чистой перешивки. Идемпотентен: доливает
# только то, чего на роутере нет, ничего существующего не перезаписывает,
# /etc/config и gl_home.html не трогает (gl_home.html пере-патчит deploy/install.sh).
#
# Сделан по образцу flint2-backups/restore-after-upgrade.sh (уроки апгрейдов
# flint2 2026-06-21 и 2026-09-28): keep-list сохраняет файлы, но НЕ сохраняет
# rc.d-симлинки (enable), opkg-пакеты и патч gl_home.html — это делает скрипт.
#
# Запуск с Mac (из корня репо beryl-xray-web-console):
#   scripts/restore-after-upgrade.sh --dry-run            # показать, что будет сделано
#   scripts/restore-after-upgrade.sh                      # выполнить
#   scripts/restore-after-upgrade.sh --no-opkg            # без opkg (нет доступа к репо GL)
#   scripts/restore-after-upgrade.sh --no-luci            # не ставить LuCI-пакеты
#   scripts/restore-after-upgrade.sh --tarball FILE       # явный tarball (backups/beryl-*.tar.gz
#                                                         #   или *.tar.gz.enc из R2 + --pass FILE)
#   scripts/restore-after-upgrade.sh --host root@192.168.8.1   # после чистой прошивки
#   scripts/restore-after-upgrade.sh --force-model            # снапшот от другой модели (mt3000 ↔ mt3600be), осознанно
#
# Два Beryl (mt3000 = AX, mt3600be = Beryl 7) пишут снапшоты в одну папку/префикс:
# скрипт берёт свежий backups/beryl-<модель>-*.tar.gz по модели целевого роутера
# (/proc/gl-hw-info/model) и сверяет host:/model: в манифесте любого tarball.
#
# После него: deploy/install.sh <host>  (пере-патч gl_home.html кнопкой панели).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST=beryl
DRY=0
OPKG=1
LUCI=1
TARBALL=""
PASSFILE=""
FORCE_MODEL=0
# Пакеты поверх стока (из /etc/backup/packages-user-installed.txt на 2026-10-25).
# zoneinfo-asia ставится отдельно, если его нет (Go-бинари читают /tmp/localtime).
PKGS="htop"
LUCI_PKGS="luci luci-app-firewall luci-app-opkg luci-compat luci-i18n-base-en luci-i18n-firewall-en luci-i18n-opkg-en luci-i18n-base-zh-cn luci-i18n-firewall-zh-cn luci-i18n-opkg-zh-cn luci-proto-ipv6 luci-proto-ppp luci-theme-bootstrap"

while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run) DRY=1 ;;
        --no-opkg) OPKG=0 ;;
        --no-luci) LUCI=0 ;;
        --tarball) TARBALL="$2"; shift ;;
        --pass) PASSFILE="$2"; shift ;;
        --host) HOST="$2"; shift ;;
        --force-model) FORCE_MODEL=1 ;;
        -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
        *) echo "unknown arg: $1"; exit 2 ;;
    esac
    shift
done

# Файлы, которые доливаем из tarball, если их нет на роутере (без ведущего /).
RESTORE_IF_MISSING="
usr/bin/sing-box
usr/bin/xray-panel-cli
etc/init.d/sing-box
etc/init.d/xray-panel-cli
etc/hotplug.d/button/50-sing-box-switch
etc/sing-box/config.json
etc/xray/config.json
etc/xray-panel-cli/panel.yaml
etc/xray-panel-cli/profiles.json
etc/xray-panel-cli/sources.json
etc/xray-panel-cli/scans
etc/rclone/rclone.conf
etc/beryl-backup.pass
usr/sbin/beryl-config-backup
etc/sysctl.d/99-disable-mptcp.conf
www/xray-panel-launcher.js
etc/opkg/customfeeds.conf
etc/opkg/keys/94cc2a834fb0aa03
etc/AdGuardHome/config.yaml
etc/dropbear/authorized_keys
etc/crontabs/root
usr/sbin/gl-unhide-cn-menus
etc/backup/packages-user-installed.txt
etc/backup/services-disabled-vs-rom.txt
"

# Пути, которые должны быть в /etc/sysupgrade.conf (чтобы следующий апгрейд сохранил их сам).
KEEP_PATHS="
/etc/xray/
/etc/sing-box/
/etc/xray-panel-cli/panel.yaml
/etc/xray-panel-cli/profiles.json
/etc/xray-panel-cli/sources.json
/etc/init.d/sing-box
/etc/init.d/xray-panel-cli
/etc/hotplug.d/button/50-sing-box-switch
/usr/bin/sing-box
/usr/bin/xray-panel-cli
/www/xray-panel-launcher.js
/etc/rclone/
/etc/beryl-backup.pass
/etc/beryl-backup/
/etc/backup/
/usr/sbin/beryl-config-backup
/usr/sbin/gl-unhide-cn-menus
/etc/sysctl.d/99-disable-mptcp.conf
/etc/opkg/keys/
/root/
/usr/share/zoneinfo/Asia/Tbilisi
"

ENABLE_SERVICES="sing-box xray-panel-cli"
# Стоковые сервисы, выключенные нами (на 2026-10-25). Если в tarball есть
# etc/backup/services-disabled-vs-rom.txt, список берётся оттуда.
DISABLE_SERVICES_DEFAULT="odhcpd rtty samba4 tor"
CRON_LINE='0 4 * * 0 /usr/sbin/beryl-config-backup'

say() { printf '\n=== %s\n' "$*"; }

TMP=$(mktemp -d)
trap 'rm -rf "${TMP:?}"' EXIT

# ---------- 0. ssh + модель целевого роутера ----------
say "ssh $HOST"
if ! OUT=$(ssh -o BatchMode=yes -o ConnectTimeout=8 "$HOST" 'cat /etc/glversion 2>/dev/null; uptime' 2>&1); then
    echo "$OUT" | head -5
    if echo "$OUT" | grep -q 'REMOTE HOST IDENTIFICATION HAS CHANGED'; then
        echo "host-ключи сменились (чистая прошивка?). Выполни: ssh-keygen -R 192.168.200.1 && ssh-keygen -R beryl, затем повтори."
    fi
    echo "после чистой прошивки: LAN = 192.168.8.1, пароль admin задаётся мастером, ssh root@192.168.8.1 → --host root@192.168.8.1"
    exit 1
fi
echo "$OUT"
MODEL=$(ssh -o BatchMode=yes "$HOST" 'cat /proc/gl-hw-info/model 2>/dev/null' | tr -cd 'a-z0-9')
[ -n "$MODEL" ] || { echo "не удалось прочитать модель (/proc/gl-hw-info/model)"; exit 1; }
echo "модель: $MODEL"

# ---------- 1. tarball (по модели) ----------
if [ -z "$TARBALL" ]; then
    TARBALL="$(ls -t "$ROOT"/backups/beryl-"$MODEL"-*.tar.gz 2>/dev/null | head -1 || true)"
    [ -n "$TARBALL" ] || { echo "нет снапшота для модели $MODEL (backups/beryl-$MODEL-*.tar.gz). Старые файлы без модели в имени — только явно: --tarball FILE"; exit 1; }
fi
[ -f "$TARBALL" ] || { echo "tarball не найден: $TARBALL"; exit 1; }
say "tarball: $TARBALL"
case "$TARBALL" in
    *.enc)
        [ -n "$PASSFILE" ] && [ -s "$PASSFILE" ] || { echo "для .enc нужен --pass FILE (1Password «system-backup pass»)"; exit 1; }
        openssl enc -d -aes-256-cbc -pbkdf2 -pass "file:$PASSFILE" -in "$TARBALL" -out "$TMP/snap.tar.gz" || { echo "расшифровка не удалась"; exit 1; }
        TARBALL="$TMP/snap.tar.gz"
        echo "расшифрован во временный файл"
        ;;
    *)
        SUMFILE="$TARBALL.sha256"
        if [ -f "$SUMFILE" ]; then
            (cd "$(dirname "$TARBALL")" && shasum -a 256 -c "$(basename "$SUMFILE")") || { echo "sha256 НЕ совпал, стоп"; exit 1; }
        else
            echo "(файла .sha256 рядом нет, контроль пропущен)"
        fi
        ;;
esac
INTAR=$(tar -tzf "$TARBALL")

# модель внутри снапшота (манифест Pipeline 3 или Pipeline 2) должна совпасть с роутером
MAN=$(tar -xzf "$TARBALL" -O tmp/backup-manifest.txt 2>/dev/null || tar -xzf "$TARBALL" -O tmp/beryl-backup-manifest.txt 2>/dev/null || true)
TB_HOST=$(printf '%s\n' "$MAN" | sed -n 's/^host: *//p' | head -1)
TB_MODEL=$(printf '%s\n' "$MAN" | sed -n 's/^model: *//p' | head -1)
[ -n "$TB_MODEL" ] || TB_MODEL=$(printf '%s' "$TB_HOST" | tr 'A-Z' 'a-z' | sed -E 's/^gl-//; s/[^a-z0-9]//g')   # GL-MT3000 → mt3000
if [ -z "$TB_MODEL" ]; then
    echo "в tarball нет манифеста с host:/model: — не могу проверить модель"; [ "$FORCE_MODEL" = 1 ] || exit 1
elif [ "$TB_MODEL" != "$MODEL" ]; then
    if [ "$FORCE_MODEL" = 1 ]; then
        echo "ВНИМАНИЕ: снапшот от модели '$TB_MODEL' (host $TB_HOST), роутер — '$MODEL'. Продолжаю по --force-model: network/wireless не трогаем, но список выключаемых сервисов и пакетов — чужой."
    else
        echo "СТОП: снапшот от модели '$TB_MODEL' (host $TB_HOST), а роутер — '$MODEL'. Укажи снапшот своей модели (--tarball) или --force-model."
        exit 1
    fi
else
    echo "модель снапшота совпадает: $TB_MODEL ($TB_HOST)"
fi

# ---------- 2. что отсутствует ----------
say "проверка отсутствующих файлов на роутере"
MISSING=$(ssh "$HOST" "for f in $(echo $RESTORE_IF_MISSING); do [ -e \"/\$f\" ] || echo \"\$f\"; done")
if [ -z "$MISSING" ]; then
    echo "все файлы на месте, доливать нечего"
else
    echo "$MISSING" | sed 's/^/  missing: /'
    TAKE=""
    for f in $MISSING; do
        if echo "$INTAR" | grep -qx "$f"; then TAKE="$TAKE $f"; else echo "  (в tarball нет: $f)"; fi
    done
    if [ -n "$TAKE" ]; then
        mkdir -p "$TMP/stage"
        # shellcheck disable=SC2086
        tar -xzf "$TARBALL" -C "$TMP/stage" $TAKE
        say "будет залито на роутер:"; (cd "$TMP/stage" && find . -type f | sed 's|^\./|  /|')
        if [ "$DRY" = 0 ]; then
            # --uid/--gid 0: bsdtar on the Mac would otherwise record the local user (501)
            # and busybox tar on the router restores that owner — dropbear then refuses
            # authorized_keys, and configs end up owned by a non-existent uid (lesson 2026-10-26).
            tar -czf - --uid 0 --gid 0 --uname root --gname root -C "$TMP/stage" . | ssh "$HOST" 'cat > /tmp/beryl-restore.tar.gz'
            ssh "$HOST" 'cd / && tar -xzpf /tmp/beryl-restore.tar.gz && rm -f /tmp/beryl-restore.tar.gz && chown -R root:root /etc/dropbear /etc/config 2>/dev/null; echo "залито"'
        fi
    fi
fi

# список выключенных сервисов — из tarball, если есть
DISABLE_SERVICES="$DISABLE_SERVICES_DEFAULT"
if echo "$INTAR" | grep -qx "etc/backup/services-disabled-vs-rom.txt"; then
    DISABLE_SERVICES="$(tar -xzf "$TARBALL" -O etc/backup/services-disabled-vs-rom.txt | tr '\n' ' ')"
fi

# ---------- 3. удалённая фаза ----------
say "удалённая фаза (DRY=$DRY OPKG=$OPKG LUCI=$LUCI)"
# shellcheck disable=SC2087
ssh "$HOST" "DRY=$DRY OPKG=$OPKG LUCI=$LUCI PKGS='$PKGS' LUCI_PKGS='$LUCI_PKGS' ENABLE_SERVICES='$ENABLE_SERVICES' DISABLE_SERVICES='$DISABLE_SERVICES' CRON_LINE='$CRON_LINE' KEEP_PATHS='$(echo $KEEP_PATHS)' sh -s" <<'REMOTE'
run() { echo "+ $*"; [ "$DRY" = 1 ] || eval "$*"; }
ex() { [ -e "$1" ]; }

echo "--- права"
for f in /usr/bin/sing-box /usr/bin/xray-panel-cli /etc/init.d/sing-box /etc/init.d/xray-panel-cli /etc/hotplug.d/button/50-sing-box-switch /usr/sbin/beryl-config-backup /usr/sbin/gl-unhide-cn-menus; do ex "$f" && run chmod 755 "$f"; done
for f in /etc/xray-panel-cli/panel.yaml /etc/xray-panel-cli/profiles.json /etc/xray-panel-cli/sources.json /etc/beryl-backup.pass /etc/rclone/rclone.conf; do ex "$f" && run chmod 600 "$f"; done

echo "--- /etc/sysupgrade.conf (keep-list)"
for p in $KEEP_PATHS; do grep -qx "$p" /etc/sysupgrade.conf 2>/dev/null || run "echo '$p' >> /etc/sysupgrade.conf"; done

if [ "$OPKG" = 1 ]; then
    echo "--- opkg"
    WANT="$PKGS"
    [ "$LUCI" = 1 ] && WANT="$WANT $LUCI_PKGS"
    ex /usr/share/zoneinfo/Asia/Tbilisi || WANT="$WANT zoneinfo-asia"
    NEED=""
    for pkg in $WANT; do opkg list-installed 2>/dev/null | grep -q "^$pkg " || NEED="$NEED $pkg"; done
    if [ -n "$NEED" ]; then
        run opkg update
        for pkg in $NEED; do run opkg install "$pkg" || echo "  (не поставился: $pkg)"; done
    else
        echo "все пакеты уже стоят"
    fi
fi

echo "--- CN-юнит: вернуть VPN/AdGuard в меню GL (идемпотентно, прошивка привозит новые JSON)"
ex /usr/sbin/gl-unhide-cn-menus && run /usr/sbin/gl-unhide-cn-menus

echo "--- localtime для Go-бинарей (sing-box, xray-panel-cli)"
if [ "$(readlink /tmp/localtime 2>/dev/null)" != "/usr/share/zoneinfo/Asia/Tbilisi" ]; then
    ex /usr/share/zoneinfo/Asia/Tbilisi && run /etc/init.d/system restart || echo "  нет /usr/share/zoneinfo/Asia/Tbilisi (zoneinfo-asia не стоит?)"
else
    echo "  /tmp/localtime → Asia/Tbilisi, ок"
fi

echo "--- sysctl (mptcp off)"
ex /etc/sysctl.d/99-disable-mptcp.conf && run sysctl -q -p /etc/sysctl.d/99-disable-mptcp.conf || true

echo "--- cron (self-backup)"
if grep -qF -- "$CRON_LINE" /etc/crontabs/root 2>/dev/null; then
    echo "  строка бэкапа на месте"
else
    run "echo '$CRON_LINE' >> /etc/crontabs/root"
    run /etc/init.d/cron restart
fi

echo "--- сервисы: enable + restart"
for s in $ENABLE_SERVICES; do
    if [ -x "/etc/init.d/$s" ]; then
        /etc/init.d/$s enabled 2>/dev/null || run "/etc/init.d/$s enable"
        run "/etc/init.d/$s restart"
    else
        echo "  нет /etc/init.d/$s"
    fi
done

echo "--- сервисы: disable (были выключены до апгрейда: $DISABLE_SERVICES)"
for s in $DISABLE_SERVICES; do
    if [ -x "/etc/init.d/$s" ]; then
        if /etc/init.d/$s enabled 2>/dev/null; then run "/etc/init.d/$s disable"; run "/etc/init.d/$s stop"; else echo "  $s уже выключен"; fi
    fi
done

echo "--- smoke"
sleep 3
echo "glversion:        $(cat /etc/glversion 2>/dev/null)"
echo "sing-box:         $(/etc/init.d/sing-box status 2>/dev/null || echo '?') ; uci enabled=$(uci -q get sing-box.config.enabled) switch=$(uci -q get sing-box.config.bind_switch)"
pgrep -f /usr/bin/xray-panel-cli >/dev/null && echo "xray-panel-cli:   up" || echo "xray-panel-cli:   DOWN"
echo "panel :9092:      HTTP $(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:9092/ 2>/dev/null) (ожидаем 401/200)"
pgrep -f AdGuardHome >/dev/null && echo "AdGuardHome:      up" || echo "AdGuardHome:      down (включён? uci adguardhome)"
echo "wg client prof.:  $(uci show wireguard 2>/dev/null | grep -c '=peers')  wg server peers: $(uci show wireguard_server 2>/dev/null | grep -c '=peers')"
echo "localtime:        $(readlink /tmp/localtime 2>/dev/null) ; $(date)"
echo "cron lines:       $(crontab -l 2>/dev/null | wc -l)"
echo "button hotplug:   $(ex /etc/hotplug.d/button/50-sing-box-switch && echo ok || echo MISSING)"
for f in /usr/bin/sing-box /usr/bin/xray-panel-cli /etc/sing-box/config.json /etc/xray/config.json /etc/xray-panel-cli/panel.yaml /etc/rclone/rclone.conf /etc/beryl-backup.pass /www/xray-panel-launcher.js; do ex "$f" && echo "ok:               $f" || echo "MISSING:          $f"; done
grep -q xray-panel-launcher /www/gl_home.html 2>/dev/null && echo "gl_home patch:    present" || echo "gl_home patch:    ABSENT -> deploy/install.sh <host>"
echo "sysupgrade -l:    $(sysupgrade -l 2>/dev/null | grep -cE 'sing-box|xray-panel-cli') путей sing-box/panel (ожидаем >=7)"
echo "rc_upgrade:       $(uci -q get upgrade.general.rc_upgrade) (0 = RC-канал выкл, иначе панель 4.8.x не видит обновлений)"
REMOTE

say "готово. Дальше: cd $ROOT && deploy/install.sh $HOST  (пере-патч gl_home.html), затем проверки UI из UPGRADE-PLAN-4.11.0.md."
