# Handoff: GL.iNet MT3000 (Beryl) — sing-box как L3-VPN-клиент

> Этот файл — историческая запись о том, что и почему сделано. Текущая полная документация (как ставить, как управлять, поведенческая матрица) — в [README.md](README.md).

## Исходная задача

Тревел-роутер **GL.iNet MT3000 (Beryl)** должен работать как полноценный L3-VPN-клиент к домашнему **Flint 2** для обхода блокировок и DPI: весь трафик всех клиентов LAN (проводных + Wi-Fi) автоматически уходит в шифрованный тоннель и выходит в интернет уже через Flint 2.

## Ограничения

- Без замены оригинальной прошивки GL.iNet
- Без правок штатных правил firewall/NAT GL.iNet (только дополнения с более ранним приоритетом)
- Решение должно переживать обновления прошивки и ребуты
- Поведение — как у штатных WG/OVPN: VPN OFF = обычный роутер, VPN ON = весь трафик в трубу, killswitch — независимый toggle

## Что было неверно в первоначальной постановке

В первой версии этого документа предлагался "XRAY TUN-режим" как Вариант 1. **Это распространённое заблуждение** — `xray-core` (включая v1.5.x, что стояло на роутере, и актуальный v1.8.x+) **не имеет встроенного `tun` inbound** и никогда его не имел. Нативную поддержку TUN дают только:

- **`sing-box`** — выбран в этом проекте (форк, поддерживает VLESS+Reality+Vision)
- `mihomo` (clash-meta)
- Связки `tun2socks` / `hev-socks5-tunnel` + xray в SOCKS-режиме

Вариант 2 ("transparent proxy через `/etc/firewall.user` + tproxy") был бы рабочим, но требует ручной возни с iptables-правилами и мешает GL.iNet-инфраструктуре. От него отказались в пользу sing-box+TUN.

## Что было исходно установлено на роутере

```
xray-core 1.5.9-1                    # бинарник от 18 июля 2022, древний
xray-geodata 1.5.9-1
v2raya 2.2.7.4-r1 + luci-app-v2raya
mptun-core 2024-01-04
iptables-mod-tproxy, kmod-ipt-tproxy, kmod-tun
```

Активного VPN на момент анализа не было: `xray` и `v2raya` оба `disabled '0'` в UCI и не запущены в `ps`. Конфиг v2raya на диске содержал VLESS+Reality на старый порт `8443`.

Все эти пакеты удалены при cleanup'е (фаза 1).

## Принятое решение

Вместо xray-core поставлен **`sing-box` 1.13.11**, кросс-собранный локально как статичный musl-совместимый бинарник (CGO disabled). Официальные релизы SagerNet линкуются с glibc (libcronet bundle) и на OpenWrt не работают.

Архитектура — в [README.md → Архитектура](README.md#архитектура). Кратко:

- **TUN inbound** `sing-tun`, gvisor-stack, MTU 1400 (system stack на musl/OpenWrt молча дропает TCP).
- **VLESS+Reality+Vision outbound** на новый порт `9443`.
- **DNS:** `hijack-dns` action в TUN, IP-private → direct, остальное — через прокси на 1.1.1.1. DNS upstream от dnsmasq идёт через direct (иначе bootstrap-loop).
- **LAN-routing (Variant X — интеграция с GL.iNet'овой инфраструктурой):** добавляются `ip rule iif br-lan lookup 2022 priority 5000` (раньше GL.iNet'овых mark-rules) + `iptables FORWARD ACCEPT br-lan ↔ sing-tun` + TCPMSS clamp. Штатные правила GL.iNet (`gl_vpn_rules` в `/etc/config/network`, `ROUTE_POLICY` в mangle) **не трогаются**.
- **Killswitch:** независимый UCI-флаг → `ip rule iif br-lan blackhole priority 5500`. По умолчанию OFF (как в GL.iNet UI). Если ON — при остановке тоннеля LAN дропается.
- **Физический переключатель** на корпусе: hotplug-хук `/etc/hotplug.d/button/50-sing-box-switch` реагирует только при `bind_switch=1` И когда штатный `switch-button.func` не привязан к WG/OVPN/Tor (чтобы не мешать).
- **Уплинк:** `auto_detect_interface: true` — sing-box подхватывает любой текущий WAN (USB-tethering / Wi-Fi-репитер / ethernet WAN).

## Что в репо

```
router/etc/sing-box/config.example.json     # шаблон с placeholders
router/etc/config/sing-box                  # UCI defaults
router/etc/init.d/sing-box                  # procd init с extra-commands
router/etc/hotplug.d/button/50-sing-box-switch
scripts/build-sing-box.sh                   # кросс-компил под musl/aarch64
scripts/install.sh                          # деплой через scp
```

Реальный `config.json` с VLESS UUID — git-ignored.

## Связанный проект

Серверная сторона тоннеля — на Flint 2: [`flint2-xray-web-console`](../flint2-xray-web-console).

## Что дальше

Веб-консоль для управления (фаза 2) — см. [README.md → Roadmap](README.md#roadmap).
