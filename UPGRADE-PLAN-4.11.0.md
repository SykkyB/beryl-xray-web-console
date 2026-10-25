# Beryl AX (GL-MT3000) — план апгрейда 4.8.1 → 4.11.0

Подготовлено: 2026-10-25. Текущая прошивка: **4.8.1 release8** (2025-08-19). Цель: **4.11.0 release3** (собрана 2026-09-24, выложена 2026-09-28).
Сделано по образцу `flint2-backups/UPGRADE-PLAN-4.11.0.md` и `restore-after-upgrade.sh`, чтобы уроки апгрейдов flint2 (2026-06-21 U-Boot recovery + 4 часа руками; 2026-09-28 keep-list + скрипт = 4 минуты) не повторять заново.
**Апгрейд ещё не выполнялся.** Этот файл — подготовка.

---

## 0. TL;DR / Go-No-Go

- **Бэкапы сняты 2026-10-25** (§2): роутерный (R2, шифр.) и Mac-овский (plaintext + sha256 + R2 шифр.). Роутерный снапшот скачан из R2, расшифрован и проверен: 153 записи, все ключевые файлы на месте.
- **`/etc/sysupgrade.conf` дополнен нашими путями** (§1.2): `sysupgrade -l` = 146 записей, 21 наш. До этого стандартный keep-list 4.8.1 НЕ сохранял ни sing-box, ни xray-panel-cli, ни rclone/pass, ни launcher.
- **`scripts/restore-after-upgrade.sh`** (§6) закрывает остальное: rc.d enable/disable, opkg (htop + LuCI), zoneinfo/localtime, cron, keep-list, smoke-тест. `--dry-run` 2026-10-25 прошёл чисто.
- **Локальные образы** (§3): `home-lab/beryl-backups/fw/` — 4.11.0 `.tar` (sha256 сверен с официальным `list-sha256.txt`), 4.8.1 `.tar` для отката, `.img` обеих версий для U-Boot web-recovery.
- **Почему панель «не видела» обновление:** в 4.8.1 включённый RC-канал (`upgrade.general.rc_upgrade=1`) тянет `fw.gl-inet.com/firmware/mt3000/rc/list-sha256.txt`, которого больше нет (404 HTML) → проверка молча возвращает пусто. RC выключен через `upgrade.set_config` 2026-10-25, панель видит 4.11.0. Не включать обратно.
- **Риски 4.11.0 на MT3000 (форум GL, октябрь 2026):** WG-клиент виснет при Tailscale + IP Masquerading (staff: фикс в 4.11.1; у нас Tailscale не залогинен); у части пользователей после прошивки не поднимались Wi-Fi/админка, помогали несколько 4-секундных reset; lockout IoT-правилом при LAN 192.168.10.1 (staff: починено в release3; у нас 192.168.200.1).
- **Рекомендация:** делать дома, с Beryl на столе и кабелем под рукой, **Keep Settings = ВКЛ**. Не ребутать повторно «на всякий случай» (урок flint2 2026-06-21).

---

## 1. Что на роутере кастомного и что переживёт апгрейд

Инвентарь снят с `/overlay/upper` и сверен с `/rom` (стоковые копии отброшены).

### 1.1 Переживает keep-settings само (стандартный keep-list 4.8.1, `sysupgrade -l`)

| Что | Где |
|---|---|
| Все UCI: network (LAN 192.168.200.1/24), wireless, repeater (апстрим к flint2), dhcp, firewall, **wireguard** (3 клиентских профиля), **wireguard_server** (1 пир), **sing-box** (uci-часть), **switch-button**, system (TZ Asia/Tbilisi, ntp), adguardhome, tailscale, gl-cloud, upgrade (rc_upgrade=0), … | `/etc/config/*`, `/etc/config/gl_nas/` |
| AdGuard Home конфиг | `/etc/AdGuardHome/config.yaml` |
| SSH authorized_keys + host-ключи dropbear | `/etc/dropbear/` |
| root-пароль, юзеры | `/etc/passwd`, `/etc/shadow`, `/etc/group` |
| cron (2 строки: gl-update-cable-mac, beryl-config-backup) | `/etc/crontabs/root` |
| opkg custom feed (v2raya) | `/etc/opkg/customfeeds.conf` |
| TLS-сертификаты админки | `/etc/uhttpd.crt|key`, `/etc/nginx/nginx.cer|key` |
| имена клиентов GL UI | `/etc/oui-tertf/client.db` |

### 1.2 НЕ было в keep-list → **добавлено в `/etc/sysupgrade.conf` 2026-10-25**

| Что | Путь |
|---|---|
| sing-box бинарь (35 МБ) + конфиг + init | `/usr/bin/sing-box`, `/etc/sing-box/`, `/etc/init.d/sing-box` |
| xray-panel-cli бинарь (10 МБ) + init | `/usr/bin/xray-panel-cli`, `/etc/init.d/xray-panel-cli` |
| конфиг панели, профили VLESS, VPN Scout sources | `/etc/xray-panel-cli/panel.yaml`, `profiles.json`, `sources.json` (**`scans/` сознательно не в keep-list**: 22 МБ, есть в бэкапе) |
| xray client config (socks 1080 → vless) | `/etc/xray/` |
| хук боковой кнопки | `/etc/hotplug.d/button/50-sing-box-switch` |
| sidebar-лаунчер панели | `/www/xray-panel-launcher.js` (патч `gl_home.html` НЕ сохраняем — пере-накатывает `deploy/install.sh`) |
| бэкап: rclone.conf, пароль шифрования, state, списки | `/etc/rclone/`, `/etc/beryl-backup.pass`, `/etc/beryl-backup/`, `/etc/backup/`, `/usr/sbin/beryl-config-backup` |
| sysctl mptcp off | `/etc/sysctl.d/99-disable-mptcp.conf` |
| ключ фида v2raya | `/etc/opkg/keys/` |
| /root (там `.config`, мусорный `'import`) | `/root/` |
| зона времени для Go-бинарей | `/usr/share/zoneinfo/Asia/Tbilisi` |

Резервная копия старого файла: `/etc/sysupgrade.conf.bak-2026-10-25`. Проверка: `ssh beryl "sysupgrade -l | grep -cE 'sing-box|xray-panel-cli'"` → 10.
Объём keep-list ≈ 46 МБ (RAM 491 МБ, available ~285 МБ — как на flint2 с 42 МБ, ок).

### 1.3 Всё равно теряется и восстанавливается скриптом (§6)

- **rc.d-симлинки:** enable `sing-box`, `xray-panel-cli`; disable `odhcpd rtty samba4 tor` (были выключены нами, списки в `/etc/backup/services-*.txt`).
- **opkg-пакеты, поставленные руками (27):** `htop` + LuCI-стек (`luci luci-app-firewall luci-app-opkg luci-compat luci-i18n-* luci-mod-* luci-proto-* luci-theme-bootstrap` + libs). Удалённый стоковый `mptun-core` после прошивки вернётся — не важно.
- **Патч `/www/gl_home.html`** (кнопка панели): `deploy/install.sh beryl`.
- `/tmp/localtime` → Asia/Tbilisi: в 4.8.1 `zoneinfo-asia` стоковый, в 4.11 проверить; скрипт доставит и сделает `/etc/init.d/system restart`.
- `sysctl` mptcp, права 600/755 — скрипт.

---

## 2. Бэкапы (состояние на 2026-10-25)

| Что | Где | Отметка |
|---|---|---|
| **Роутерный снапшот** (Pipeline 2, v3-скрипт, шифр.) | R2 `r2:sys-lab-home-backups/beryl-snapshots/beryl-self-20261025-183323Z.tar.gz.enc` | 19 275 312 байт; скачан, расшифрован, 153 записи — проверено |
| **Mac-снапшот** (Pipeline 3, plaintext) | `beryl-xray-web-console/backups/beryl-20261025-183812Z.tar.gz` | 19 275 359 байт, sha256 `2e4d10107a57728f6193bc86a78be99e5ca5a40b9e9e7fc59faa32de628e3ed4` (файл `.sha256` рядом) |
| то же, шифр. | R2 `beryl-snapshots/beryl-20261025-183812Z.tar.gz.enc` | 19 275 376 байт |
| предыдущие | R2/Mac `beryl-*20260622*` | исторические (скоуп уже) |

В обоих снапшотах манифест: glversion, `uci export system` (TZ/ntp), счётчики VPN, списки `packages-user-installed` / `packages-removed-vs-rom` / `services-disabled-vs-rom` / `sysupgrade-keep-list`, сам `sysupgrade.conf`.
Расшифровка R2-копий: `openssl enc -d -aes-256-cbc -pbkdf2 -pass file:<pass> -in <file>.enc -out <file>` (pass = `beryl:/etc/beryl-backup.pass` = 1Password «system-backup pass»).

Чего в бэкапах **нет** (делать руками при чистой перешивке): мастер первичной настройки GL (admin-пароль; LAN потом из `/etc/config/network`), привязка GoodCloud (в `gl-cloud`, может потребовать перелогин), Tailscale (не залогинен — нечего терять), включение sing-box тумблером/кнопкой.

---

## 3. Прошивка 4.11.0 — факты

Из `fw.gl-inet.com/firmware/mt3000/release/metadata_4.11.0` и `list-sha256.txt`:

- Файл: `mt3000-4.11.0_release3-1147-0924-1790238340.tar`, **80 408 765 байт**, sha256 `7f0cf627d6ad778c11ae0fefe668ac3c6eedd3164f6c215281649b2656afcc0e`.
- База: `OpenWrt 21.02-SNAPSHOT r15812+926-46b6ee7ffc`, target `mediatek/mt7981`, board `glinet_gl-mt3000`, тип `release3`, сборка 2026-09-24 15:48:49. Закрытый MTK-драйвер, как и 4.8.1 (op24/op25 — отдельный бета-канал, не наш).
- `upgrade_control` пустой, канал stable — серверных ограничений выката нет.
- Локальные копии: `home-lab/beryl-backups/fw/mt3000-4.11.0_release3-*.tar` (+`.sha256`, сверен), `mt3000-4.8.1-0819-1755615825.tar` (откат, sha256 `ee038ee0…`), `*.img` обеих версий для U-Boot (хэши в `.sha256` рядом, официальных нет).
- 4.11.1 beta1 (2026-09-29) чинит только HTTPS к админке по DDNS/MagicDNS; фикс WG+Tailscale обещан «скорее всего в 4.11.1».

Релиз-ноты: Mesh (EasyMesh, главный роутер только в Router mode; узел теряет свой IP/подсеть и VPN), AmneziaWG client+server (2.0 с I1–I5, вводятся вручную), Network Quality, Security Scan, VLAN 802.1Q, UPnP-тумблер, DNS-дашборд, X-VPN, GoodPAS.

Что может задеть нас:

| Изменение | Риск | Что проверить после |
|---|---|---|
| DNS-дашборд (WAN/VPN/Manual/Encrypted) | средний: sing-box tun со своим DNS (1.1.1.1 через туннель) + `gl-dns override_vpn=1` | `uci show gl-dns`; с VPN вкл: `nslookup` через клиента; AdGuard (сейчас выключен) не включился сам |
| Repeater к flint2 (wwan `apclix0`) | низкий-средний: wireless/repeater мигрируют | после бута интернет есть, `ubus call network.interface.wwan status` up |
| Новый `gl_home.html` | ожидаемо: патч теряется | `deploy/install.sh beryl`, Cmd+Shift+R |
| LuCI | 4.11 может привезти свой luci или сломать наш набор | `opkg list-installed \| grep -c luci`; скрипт доставит недостающее |
| Кнопка сбоку (switch-button + hotplug) | низкий | переключить туда-обратно, `logread \| grep sing-box` |
| WG+Tailscale deadlock | нулевой пока Tailscale не залогинен | не включать IP Masquerading до 4.11.1 |
| Wi-Fi/админка не поднялись после прошивки (форум) | низкий-средний | 4-сек reset (network repair), повторить до 3 раз; потом U-Boot |
| Mesh-страница | только если трогать | **не переводить в узел**, пока не решено (теряет 192.168.200.x и VPN) |

---

## 4. Прямо перед апгрейдом (5 минут)

```sh
cd ~/Documents/projects/home-lab/beryl-xray-web-console
# 1. Свежие снапшоты обоих pipeline (hash-skip обходим --force)
ssh beryl '/usr/sbin/beryl-config-backup --force; tail -3 /etc/beryl-backup/beryl-backup.log'
scripts/backup.sh beryl
# 2. keep-list видит наши пути, RAM есть
ssh beryl "sysupgrade -l | grep -cE 'sing-box|xray-panel-cli|rclone|beryl-backup'; free | head -2"
# 3. Baseline (сравнить после)
ssh beryl "cat /etc/glversion; /etc/init.d/sing-box status; uci -q get sing-box.config.enabled; uci show wireguard | grep -c =peers; crontab -l | wc -l; opkg list-installed | wc -l; curl -s -o /dev/null -w 'panel %{http_code}\n' http://127.0.0.1:9092/"
# 4. sha256 локального образа (если будет Local Upgrade)
(cd ../beryl-backups/fw && shasum -a 256 -c mt3000-4.11.0_release3-1147-0924-1790238340.tar.sha256)
```

Beryl — репитер за flint2, домашняя сеть от него не зависит; побочных алертов (watchdog/exit1.dev) не будет.

---

## 5. Апгрейд

**Способ А:** Admin Panel → System → Upgrade → Firmware Online Upgrade (теперь показывает 4.11.0) → **Keep Settings ВКЛ** → Upgrade. 80 МБ, sha256 сверяет GL. 3–5 минут.

**Способ Б:** Firmware Local Upgrade → `beryl-backups/fw/mt3000-4.11.0_release3-1147-0924-1790238340.tar` (sha256 из §3) → Keep Settings ВКЛ → Install.

**Не делать:** `opkg upgrade` до или после; повторный ребут «на всякий случай»; Reset settings; включать RC-канал.

**Если не поднялся за 10 минут** (нет Wi-Fi Beryl / 192.168.200.1, нет ssh):
1. Подождать ещё 5 минут (первый бут мигрирует конфиги).
2. Короткий reset ~4 с (network repair) — форумный опыт на 4.11.0: Wi-Fi возвращался после 1–3 таких нажатий, настройки оставались.
3. U-Boot web-recovery: ноут кабелем в LAN, статика `192.168.1.2/24`; выключить, зажать RESET, включить, держать до частого мигания LED → `http://192.168.1.1` → залить **`.img`** из `beryl-backups/fw/`. Это чистая прошивка (LAN 192.168.8.1, мастер задаёт пароль). На форуме есть отчёт, что загрузка в U-Boot зависала на 100 % (тема 70575) — при повторе пробовать другой браузер/кабель.

---

## 6. После апгрейда

### 6.1 Скрипт (5 минут)

```sh
cd ~/Documents/projects/home-lab/beryl-xray-web-console
scripts/restore-after-upgrade.sh --dry-run     # показать, что будет сделано
scripts/restore-after-upgrade.sh               # выполнить
```

Что делает (идемпотентно): sha256 tarball → ssh (подсказка про `ssh-keygen -R`, если host-ключи сменились) → доливает из `backups/beryl-*.tar.gz` только то, чего на роутере нет → права → дополняет `/etc/sysupgrade.conf` → `opkg update` + `htop` + LuCI (+ `zoneinfo-asia`, если нет) → `/etc/init.d/system restart` при отсутствии `/tmp/localtime` → sysctl → строка cron → enable+restart `sing-box`, `xray-panel-cli` → disable `odhcpd rtty samba4 tor` → smoke-тест → напоминает про `deploy/install.sh`.
Флаги: `--dry-run`, `--no-opkg`, `--no-luci`, `--tarball FILE` (`.enc` из R2 + `--pass FILE`), `--host root@192.168.8.1` (после чистой прошивки).

### 6.2 Пере-патч GL UI кнопкой панели (2 минуты)

```sh
deploy/install.sh beryl
```
Не трогает существующий `panel.yaml`, кладёт launcher и `<script>` в новый `gl_home.html`. После: Cmd+Shift+R в админке.

### 6.3 Только при чистой перешивке (U-Boot), до 6.1

Cherry-pick UCI из tarball (не всё подряд, схемы 4.11 могут отличаться): `network`, `wireless`, `repeater`, `dhcp`, `firewall`, `wireguard`, `wireguard_server`, `sing-box`, `switch-button`, `system`, `adguardhome`, `dropbear` + `/etc/dropbear/authorized_keys`, `/etc/passwd|shadow|group`, `/etc/AdGuardHome/config.yaml`, `/etc/crontabs/root`, `/etc/opkg/customfeeds.conf` + `/etc/opkg/keys/`. Затем `reboot`, затем 6.1 (`--host root@192.168.8.1`, пока LAN не вернулся на 192.168.200.1) и 6.2.

### 6.4 Ручные проверки в UI (5 минут)

- Internet: репитер подключён к flint2 (`XXX.v52`/`XXX.v24`), интернет есть.
- VPN → WireGuard Client: 3 профиля на месте; VPN Dashboard: наш sing-box-профиль и кнопка сбоку переключают.
- Панель `http://192.168.200.1:9092/` → 401 → логин работает; кнопка панели в сайдбаре GL.
- System → Upgrade: показывает 4.11.0 текущей, RC выкл.
- Applications → AdGuard Home: как было (выключен); Network → UPnP выкл; DNS-дашборд: режим как до апгрейда.
- Новое: VPN → AmneziaWG client/server появились (можно настроить клиента к flint2 позже); **Mesh не трогать**.

---

## 7. Smoke-тест (делает скрипт, можно руками)

```sh
ssh beryl "cat /etc/glversion; /etc/init.d/sing-box status; pgrep -f xray-panel-cli >/dev/null && echo panel-up || echo PANEL-DOWN; \
  curl -s -o /dev/null -w 'panel %{http_code}\n' http://127.0.0.1:9092/; \
  uci show wireguard | grep -c =peers; readlink /tmp/localtime; date; crontab -l | wc -l; \
  opkg list-installed | grep -cE '^(htop|luci) '; grep -q xray-panel-launcher /www/gl_home.html && echo patch-ok || echo PATCH-ABSENT; \
  sysupgrade -l | grep -cE 'sing-box|xray-panel-cli'; uci -q get upgrade.general.rc_upgrade; \
  ubus call network.interface.wwan status | grep '\"up\"'; logread | grep -ciE 'error|fail'"
```
Ожидание: panel 401, 3 WG-профиля, localtime Asia/Tbilisi, 2 cron-строки, patch-ok, ≥7 путей keep-list, rc_upgrade 0, wwan up.

---

## 8. Откат

- UI → System → Upgrade → Local Upgrade с `beryl-backups/fw/mt3000-4.8.1-0819-1755615825.tar` (sha256 `ee038ee0…`), Keep Settings ВКЛ. После отката тоже `scripts/restore-after-upgrade.sh` (rc.d-симлинки и opkg). RC-канал оставить выключенным, иначе 4.8.1 снова «не видит обновлений».
- Полный откат к состоянию 2026-10-25 на любой прошивке: tarball §2 + `restore-after-upgrade.sh` + cherry-pick UCI (§6.3).

---

## 9. Результат

(заполнить после апгрейда: дата, способ, время восстановления, что потерялось, отклонения от плана)
