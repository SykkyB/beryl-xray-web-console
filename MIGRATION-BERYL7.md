# Перенос с Beryl AX (GL-MT3000) на Beryl 7 (GL-MT3600BE) — 2026-10-26

Выполнено 2026-10-26 18:30–20:15 UTC+4. Новый роутер занял место старого: тот же LAN 192.168.200.1, те же SSID/пароли, тот же ssh-ключ, тот же стек (sing-box + xray-panel-cli + кнопка + бэкапы). Старый Beryl AX (4.11.0) выключен, его последние снапшоты в R2: `beryl-self-20261026-073709Z` / `beryl-20261026-073740Z` (Mac `backups/`).

## 1. Факты про Beryl 7

| | |
|---|---|
| Железо | MT7987A (Filogic 850), 4× Cortex-A53 2.0 ГГц, 512 МБ RAM, 512 МБ NAND, 2× 2.5G (eth0 WAN, eth1 LAN), USB 3.0, боковой переключатель gpio-465 |
| Прошивка | была 4.8.7 release2 (2026-04-15) → **4.9.0 release1** (2026-07-07). База OpenWrt 21.02 MTK SDK, kernel 5.4.281, `aarch64_cortex-a53` — наши arm64-бинари идут без пересборки |
| Чего нет | 4.11 для MT3600BE ещё нет → **mesh не поддерживается** (Beryl 7 в списке unsupported в доках GL); новый DNS-дашборд 4.11 тоже отсутствует. AmneziaWG 2.0 есть (4.9.0) |
| Регион | **CN-юнит**, VPN-меню возвращены скриптом (см. ниже): `/proc/gl-hw-info/country_code = CN` (заводские данные, не прошивка). Образы прошивки на `fw.gl-inet.cn` и `fw.gl-inet.com` совпадают по sha256 |

### Как CN-регион реально ограничивает
- Бэкенд **ничего не блокирует**: `system.get_info` → `hidden_features: []`, `software_feature.vpn/adguard/obfuscation = true`; RPC `vpn-client`, `wg-client`, `wg-server` отвечают; пакеты wireguard/amneziawg/openvpn/adguardhome стоят и обновляются.
- Прячет только фронтенд: меню приходит из `/usr/share/oui/menu.d/*.json`, у `adguardhome, dynamicdns, ovpnserver, torview, vpn-client (wgclient/ovpnclient/vpndashboard), vpndashboard, wgserver` стоит `"lang_hide": ["zh-cn"]`. Фильтр в `app.js` сравнивает это **с кодом страны** (`countryCode.indexOf("cn") >= 0 && lang_hide.includes("zh-cn")` → скрыть), а не с языком интерфейса. Скрытые пункты выпадают и из `allowRoutes`, поэтому прямые URL не работают и `supportVpn` во всём UI = false. По коду страны отдельно прячется AstroWarp (`country: {codes:["cn"], exclude:true}`). Бейдж CN рядом с версией — оттуда же.
- Служба `/etc/init.d/vpn-client` на юните выключена (S95vpn-client есть в /rom, в rc.d нет); в init-скрипте проверки страны нет.
- Облако: `cloud.gl-inet.cn`, сервер обновлений `fw.gl-inet.cn`. Фиды opkg — международные `fw.gl-inet.com/releases/v21.02-SNAPSHOT/mt7987a/…`.
- Официальная позиция GL: конвертация CN→Global не поддерживается, метод смены региона в загрузчике на форуме удалён по просьбе GL — не делаем.
- **VPN-меню возвращены (применено 2026-10-26 20:20):** `/usr/sbin/gl-unhide-cn-menus` (в git: `router/usr/sbin/`) убирает `lang_hide` из семи JSON в `/usr/share/oui/menu.d/` (оригиналы рядом как `.orig`), перезапускает nginx и включает службу `vpn-client`. Идемпотентен, проверяет, что результат валидный JSON. Прошивка при апгрейде привозит новые JSON → скрипт вызывают `deploy/install.sh` и `restore-after-upgrade.sh`; сам скрипт в keep-list. Бейдж CN в шапке остаётся. GL это не поддерживает.

## 2. Что сделано (по порядку)
1. ssh-ключ `~/.ssh/beryl` → `/etc/dropbear/authorized_keys` (вручную с паролем), алиас `beryl7`.
2. Интернет: профиль репитера `XXX.v52` из снапшота AX добавлен в `/etc/config/repeater`, `/etc/init.d/repeater restart` → wwan `apclii0` 192.168.100.212.
3. Апгрейд 4.8.7 → 4.9.0 через RPC `upgrade.upgrade_online {keep_config:true,keep_package:true}`: скачивание 16 с, ребут 63 с.
4. Пакет переноса из `backups/beryl-20261026-073740Z.tar.gz`: `/etc/config/{wireguard,wireguard_server,ovpnclient,ovpnserver,openvpn,adguardhome,switch-button,sing-box}`, `/etc/openvpn`, `/etc/AdGuardHome/config.yaml`, `/etc/sing-box/config.json`, `/etc/xray`, `/etc/xray-panel-cli` (вкл. scans), `/etc/rclone`, `/etc/opkg/customfeeds.conf` + ключ v2raya, `/etc/sysctl.d/99-disable-mptcp.conf`, `/usr/bin/sing-box`, `/usr/sbin/beryl-config-backup` (из git), `/etc/dropbear/authorized_keys`; `/etc/beryl-backup.pass` = `flint2:/etc/system-backup.pass`.
   НЕ переносилось: network, wireless (кроме SSID/ключей), passwd/shadow, host-ключи dropbear, board.json, сертификаты админки, dhcp (резерваций не было), firewall (своих правил не было).
5. Wi-Fi: `wireless.ra0` (2.4) ← `XXXX.24`, `wireless.rai0` (5) ← `XXX.52`, шифрование оставлено заводское (psk2+ccmp / sae-mixed). На AX 5-ГГц AP был выключен, на Beryl 7 включён.
6. Keep-list: блок «custom (added 2026-10-26)» в `/etc/sysupgrade.conf` (`.bak-2026-10-26` рядом), cron-строка бэкапа (на 4.9.0 файла `/etc/crontabs/root` не было).
7. `deploy/install.sh beryl7`: сборка панели под arm64, init-скрипты, хук `/etc/hotplug.d/button/50-sing-box-switch`, launcher + патч `gl_home.html`.
8. LAN → 192.168.200.1 (`uci set network.lan.ipaddr`, `network restart` в фоне). Алиасы `beryl` и `beryl7` → 192.168.200.1, `ssh-keygen -R` для обоих IP.
9. `scripts/restore-after-upgrade.sh --host beryl`: поставил `zoneinfo-asia` (в 4.9.0 его нет → `/tmp/localtime` для Go-бинарей), `htop`, LuCI-набор как на AX; `system restart`; сервисы enable+restart.
10. Снапшоты: роутер → R2 `beryl-self-20261026-160808Z…` и финальный после пакетов; Mac → `backups/beryl-20261026-16…Z.tar.gz` + `.sha256` + R2.

## 3. Грабли (чтобы не повторять)
- **Владелец в tar.** Пакет, собранный на Mac (`bsdtar`), записал uid 501; busybox-tar на роутере от root восстановил его → `/etc/dropbear/authorized_keys` стал не root и dropbear закрыл вход по ключу (нужен был `chown` с паролем). Лечение навсегда: `tar … --uid 0 --gid 0 --uname root --gname root` (уже в `restore-after-upgrade.sh`) и `chown -R root:root` после распаковки.
- **Ручной IP на Mac.** Ethernet-адаптер Mac был настроен вручную на 192.168.8.35/16 — после смены LAN роутера «DHCP не приходит» было не про роутер. Проверять `ipconfig getsummary en6` (ConfigMethod).
- **2.5G-линк после `network restart`** не поднялся сам, помогло переткнуть кабель.
- Проверять ownership-ловушку и для `deploy/install.sh` (он копирует через `scp -O` + `cp`, владелец root — ок).
- Стандартный keep-list 4.9.0 на MT3600BE: 127 записей, наших там не было до блока.

## 4. Проверки после переноса
`ssh beryl`: glversion 4.9.0; sing-box `active with no instances` (кнопка в «выкл»), `enabled=1`, `bind_switch=1`, `switch-button.func=xray`; панель `:9092` → 401; патч gl_home present; hotplug ok; WG 3 профиля + 1 пир сервера; localtime Asia/Tbilisi; cron 1 строка; keep-list ≥ 8 наших путей; SSID XXXX.24 / XXX.52; wwan up; с Mac админка 200 и панель 401.
Руками: боковая кнопка туда-обратно (sing-box должен подняться по активному профилю), панель в сайдбаре GL, System → Toggle Button Settings (функция `xray` наша, GL может показать её как неизвестную — норма).

## 5. Откат
Старый Beryl AX полностью рабочий (4.11.0, всё на месте, последний снапшот 26.10 07:37Z). Включить, воткнуть кабель, `ssh beryl` (host-ключ AX: `ssh-keygen -R 192.168.200.1` перед первым заходом).
