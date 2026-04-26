# beryl-xray-web-console

VPN-клиент `sing-box` (XRAY-совместимый) для тревел-роутера **GL.iNet MT3000 (Beryl)** с поведением, идентичным штатным WireGuard / OpenVPN-клиентам в прошивке GL.iNet — включая интеграцию с физическим переключателем на боку и killswitch.

Репозиторий содержит конфиги, init-скрипты, hotplug-хуки и сборочные скрипты, разворачиваемые поверх стоковой прошивки GL.iNet **без её замены**. В будущем сюда же приедет веб-консоль для управления (см. [Roadmap](#roadmap)).

> Симметричный проект для серверной стороны тоннеля (на Flint 2): [`flint2-xray-web-console`](../flint2-xray-web-console).

---

## Архитектура

```
LAN client (Mac / phone / IoT)
    ↓ DHCP from beryl
br-lan (192.168.200.0/24)
    ↓
[ip rule prio 5000: iif br-lan → table 2022]
    ↓
sing-tun (gvisor stack, MTU 1400)
    ↓
sing-box (TCP/UDP terminate, route)
    ↓
VLESS+Reality+Vision  →  vpn.sys-lab.xyz:9443  (Flint 2)
    ↓
default WAN  (auto_detect_interface: usb0 / apclix0 / eth0 — то, что есть)
    ↓
Internet
```

Параллельно — fallback и killswitch:

| Состояние | Что произойдёт с LAN-трафиком |
|---|---|
| sing-box up | через `sing-tun` (`prio 5000`) → VLESS-тоннель |
| sing-box down + killswitch=off | fallthrough → mark `0x8000` от GL.iNet → `prio 6000` → main → прямой WAN |
| sing-box down + killswitch=on | `prio 5500 blackhole iif br-lan` → `Network unreachable` |
| Физический переключатель OFF + bind_switch=on | `stop_service` всё снимает → прямой WAN (или kill, см. ниже) |

### Почему не "TUN-режим xray-core"

В оригинальной [HANDOFF.md](HANDOFF.md) рассматривался "XRAY TUN-режим" — это распространённое заблуждение. **`xray-core` не имеет `tun` inbound** и никогда его не имел. Нативную поддержку TUN дают только:

- `sing-box` — выбран в этом проекте (форк, поддерживает VLESS+Reality+Vision)
- `mihomo` (clash-meta)
- Связка `tun2socks` / `hev-socks5-tunnel` + xray в SOCKS-режиме

### Почему `stack: "gvisor"`, а не `system`

На OpenWrt поверх musl `stack: "system"` молча дропает TCP-пакеты на интерфейсе TUN — пакеты доходят (`/proc/net/dev` RX растёт), но sing-box не терминирует их как TCP-соединения. `gvisor` (Go-userland TCP/IP) работает корректно. Бинарник в этом репо собран с тегом `with_gvisor`.

### Интеграция с инфраструктурой GL.iNet (Variant X)

GL.iNet имеет собственную VPN-инфраструктуру в `/etc/config/network` (правила с пометкой `gl_vpn_rules`):

```
prio 6000: from all fwmark 0x8000/0xf000 lookup main      # "no-VPN" → main
prio 9000: not from all fwmark 0/0xf000 lookup main       # marked → main
prio 9910: not from all fwmark 0/0xf000 blackhole         # marked + no route → kill
prio 9920: from all iif br-lan blackhole                  # LAN catch-all → kill
```

Также `iptables -t mangle PREROUTING` имеет `ROUTE_POLICY` → `TUNNEL100_ROUTE_POLICY`, который маркирует весь LAN-трафик `mark 0x8000` ("no-VPN, в main").

Наш init **не правит** ни одно из этих правил. Вместо этого добавляются собственные с приоритетами **меньше** (раньше) `6000`, чтобы перехватить LAN-трафик до GL.iNet'овского bypass:

```
prio 5000: from all iif br-lan lookup 2022                # → sing-tun (если sing-box up)
prio 5500: from all iif br-lan blackhole                  # killswitch (опционально)
```

Также добавляются `iptables FORWARD ACCEPT br-lan ↔ sing-tun` (sing-tun не в fw3-зоне, иначе FORWARD policy DROP) и `TCPMSS clamp` под MTU 1400.

При обновлении прошивки GL.iNet ничего из этого не сломается — мы только добавляем поверх.

---

## Структура репозитория

```
beryl-xray-web-console/
├── README.md
├── HANDOFF.md                       # исходная постановка задачи (legacy, обновлена)
├── router/etc/
│   ├── sing-box/
│   │   └── config.example.json      # шаблон конфига (UUID/PBK/SID — placeholders)
│   ├── config/
│   │   └── sing-box                 # UCI defaults (enabled/killswitch/bind_switch)
│   ├── init.d/
│   │   └── sing-box                 # procd init с расширенными командами
│   └── hotplug.d/button/
│       └── 50-sing-box-switch       # привязка к физическому переключателю
└── scripts/
    ├── build-sing-box.sh            # кросс-компиляция бинарника (musl-friendly)
    └── install.sh                   # деплой через scp/ssh
```

Реальный `router/etc/sing-box/config.json` (с твоим VLESS UUID) **в git не коммитится**. Создаётся локально из `config.example.json`.

---

## Установка с нуля

### 0. Pre-flight на роутере (одноразово)

SSH на роутер (предполагается алиас `beryl` в `~/.ssh/config`, root-доступ):

```sh
ssh beryl
```

Проверь и при необходимости поставь зависимости:

```sh
opkg update
opkg list-installed | grep -E 'kmod-tun|iptables-mod-tproxy|kmod-ipt-tproxy'
# если чего-то нет:
opkg install kmod-tun iptables-mod-tproxy kmod-ipt-tproxy
mkdir -p /etc/hotplug.d/button   # на стоковом MT3000 директория пустая, но существует
```

### 1. Сборка бинарника

На локальной машине нужен Go 1.22+. Бинарник статический, musl-совместимый — официальные релизы SagerNet линкуются с glibc и на OpenWrt не работают.

```sh
./scripts/build-sing-box.sh
# → build/sing-box-build/sing-box-static
```

Можно переопределить версию или теги:

```sh
SING_BOX_VERSION=v1.13.11 ./scripts/build-sing-box.sh
SING_BOX_TAGS="with_quic,with_grpc,with_dhcp,with_wireguard,with_utls,with_clash_api,with_gvisor" ./scripts/build-sing-box.sh
```

### 2. Локальный конфиг с твоими credentials

```sh
cp router/etc/sing-box/config.example.json router/etc/sing-box/config.json
```

Открой `router/etc/sing-box/config.json` и подставь значения из своей VLESS-строки (например, `vless://UUID@host:port?...`):

| Placeholder | Откуда взять |
|---|---|
| `__VPN_HOSTNAME__` | hostname после `@` (две позиции в файле — DNS-rule и outbound `server`) |
| `__VLESS_UUID__` | UUID после `vless://` |
| `__REALITY_SNI__` | `sni=` параметр (обычно `www.cloudflare.com`) |
| `__REALITY_PUBLIC_KEY__` | `pbk=` параметр |
| `__REALITY_SHORT_ID__` | `sid=` параметр |

### 3. Деплой на роутер

```sh
./scripts/install.sh
```

Скрипт:
- зальёт бинарник в `/usr/bin/sing-box`,
- зальёт UCI-config в `/etc/config/sing-box`,
- зальёт init в `/etc/init.d/sing-box` и hotplug в `/etc/hotplug.d/button/50-sing-box-switch`,
- зальёт `config.json` в `/etc/sing-box/config.json` с правами `0600`,
- провалидирует конфиг (`sing-box check`),
- включит и (пере)стартует сервис.

### 4. Проверка

```sh
# с роутера:
ssh beryl 'curl -sS https://api.ipify.org'
# должно выдать IP экзита Flint 2 (например, 176.221.192.204)

# с LAN-устройства (по проводу или Wi-Fi роутера):
curl https://ifconfig.me
# тот же IP экзита
```

---

## Команды управления

Стандартные procd:

```sh
/etc/init.d/sing-box start
/etc/init.d/sing-box stop
/etc/init.d/sing-box restart
/etc/init.d/sing-box reload
/etc/init.d/sing-box enable      # автостарт при загрузке
/etc/init.d/sing-box disable
```

Расширенные:

```sh
/etc/init.d/sing-box killswitch_on        # дроп LAN при падении тоннеля
/etc/init.d/sing-box killswitch_off       # автобайпас на WAN при падении (default)
/etc/init.d/sing-box killswitch_status

/etc/init.d/sing-box bind_switch_on       # привязать к физ. переключателю на корпусе
/etc/init.d/sing-box bind_switch_off
/etc/init.d/sing-box bind_switch_status
```

Все toggle-команды сохраняют состояние в UCI (`/etc/config/sing-box`) — переживают ребуты.

---

## Поведенческая матрица (как у GL.iNet WG/OVPN)

| `enabled` | `killswitch` | `bind_switch` | physical switch | LAN получает интернет... |
|---|---|---|---|---|
| 0 | — | — | — | как обычный роутер (прямой WAN) |
| 1 | 0 | 0 | — | через тоннель; при падении → автобайпас в WAN |
| 1 | 1 | 0 | — | через тоннель; при падении → обрыв (killswitch) |
| 1 | 0 | 1 | ON | через тоннель |
| 1 | 0 | 1 | OFF | как обычный роутер (прямой WAN) |
| 1 | 1 | 1 | ON | через тоннель; при падении → обрыв |
| 1 | 1 | 1 | OFF | прямой WAN (физ. OFF опережает UCI killswitch) |

**Что считается "LAN":** всё, что входит на роутер через `iif br-lan`. На GL.iNet MT3000 это и проводной LAN, и Wi-Fi 2.4/5 ГГц (бриджуются в `br-lan` стоковой прошивкой). Guest-сеть (`br-guest`), wgserver / ovpnserver bridges — **не покрываются**, добавлять явно.

**Уплинк:** `auto_detect_interface: true` — sing-box подхватывает default route автоматически. Подключил роутер к Wi-Fi отеля / переткнул USB-tethering / воткнул ethernet — тоннель сам переустановится через новый WAN.

---

## Файлы на роутере

| Путь | Назначение |
|---|---|
| `/usr/bin/sing-box` | бинарник, 35 МБ, статический |
| `/etc/sing-box/config.json` | runtime-конфиг (DNS / TUN / VLESS+Reality / route) |
| `/etc/config/sing-box` | UCI: `enabled`, `killswitch`, `bind_switch` |
| `/etc/init.d/sing-box` | procd init с extra-commands |
| `/etc/hotplug.d/button/50-sing-box-switch` | hook на физ. переключатель |
| `/var/log/sing-box.log` | логи sing-box (tmpfs, очищается при ребуте) |

---

## Диагностика

```sh
# процесс
pgrep -af sing-box

# логи
tail -f /var/log/sing-box.log
logread -e sing-box-switch          # события физ. переключателя

# таблицы маршрутов
ip rule list
ip route list table 2022             # таблица sing-box (default → sing-tun)
ip route list table main

# firewall
iptables -L FORWARD -n -v --line-numbers | head -10

# clash-API (REST + WebSocket, для будущей веб-консоли)
curl -s http://127.0.0.1:9090/version
curl -s http://127.0.0.1:9090/proxies
curl -s http://127.0.0.1:9090/connections
```

---

## Ограничения и гoтча

- **Нет nftables**, только iptables-legacy. `auto_redirect` от sing-box не используется (требует nft).
- **Не покрывает guest network и VPN-server bridges** — только `br-lan`.
- **При смене WAN** (USB-tether / Wi-Fi-репитер) есть короткая (≤5 сек) дыра, пока sing-box переустанавливает соединение через новый интерфейс. `procd respawn 3600 5 5` поднимает sing-box если он упадёт.
- **DNS upstream от роутера** идёт через `direct` outbound в обход тоннеля (`ip_is_private` matches LAN-gateway). Если хочется DNS-over-VPN — поправь route-rules.
- **Bootstrap loop** при резолве `vpn.sys-lab.xyz`: решён правилом `default_domain_resolver: local-dns` + DNS-rule `domain → local-dns` + порядком route-rules (`ip_is_private` ВЫШЕ `hijack-dns`).
- **MPTCP в kernel** OpenWrt 21.02 GL.iNet build — **сломан** для произвольных listener'ов (`subflow_v4_init_req` не реализован → SYN-ACK кривой `SRC=0.0.0.0 DST=0.0.0.0`, уходит в `lo`, LAN-клиент таймаутит). По умолчанию включён (`net.mptcp.enabled=1`). `uhttpd` патчем GL.iNet'а отключает MPTCP на сокете, поэтому стоковый UI работает; Go-listener'ы — нет. **Фикс:** `echo net.mptcp.enabled=0 > /etc/sysctl.d/99-disable-mptcp.conf` (применяется автоматически в `deploy/install.sh`).

---

## Roadmap

### Phase 1 — done
- [x] Кросс-компиляция статичного sing-box под musl/aarch64
- [x] TUN inbound + VLESS+Reality+Vision outbound
- [x] LAN routing через `iif br-lan` без правки штатных правил GL.iNet
- [x] Killswitch (UCI-флаг + GL.iNet'овский blackhole)
- [x] Привязка к физическому переключателю через hotplug
- [x] Идемпотентный init (start/stop/restart/reload)

### Phase 2 — in progress
- [x] Снос устаревших пакетов (`v2raya`, `xray-core`)
- [ ] Несколько профилей VLESS+Reality (для fail-over) через `outbound: selector` + `urltest`
- [ ] Веб-консоль `xray-panel-cli` (см. `cmd/xray-panel-cli/`):
  - [x] **2A.** Скелет: Go single-binary, embed UI, bcrypt basic-auth, LAN-bind guard, procd init, deploy-script, `/api/ping`
  - [x] **2B.** Service API: `GET /api/state` (sing-box процесс, `sing-tun`, физ. переключатель, killswitch, bind_switch, enabled), `POST /api/service`, `POST /api/killswitch`, `POST /api/bind_switch`. UI с тумблерами и кнопками управления + auto-refresh каждые 5с.
  - [ ] **2C.** Профили: парсинг `vless://` URL, CRUD, активация → пересборка `config.json` → reload
  - [ ] **2D.** Frontend: status-страница, переключатели, профили, лайв-данные через clash-API + WebSocket-логи
  - [ ] **2E.** Multi-outbound `selector` / `urltest` для real-time fail-over
  - [ ] **2F.** Стиль GL.iNet UI (шрифты/палитра/иконки из их прошивки) + интеграция через iframe / поддомен
- [ ] Симметричный апдейт для серверной стороны (`flint2-xray-web-console`)

#### Стек панели

- **Backend:** Go 1.25, single binary (~6 МБ статичный musl), embedded UI через `embed.FS`, basic-auth (bcrypt), LAN-bind guard.
- **Порт:** `9092` (на beryl 9090 занят clash-API; 80/443/8080/8443 — nginx/uhttpd/GL.iNet UI).
- **Auth:** общий bcrypt-хеш с flint2-панелью — одни логин/пароль на обе консоли.
- **Конфиг панели:** `/etc/xray-panel-cli/panel.yaml` (LAN-bind, paths, creds).
- **Сборка:** `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/xray-panel-cli`
- **Деплой:** `./deploy/install.sh beryl`
