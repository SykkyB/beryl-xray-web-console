# beryl-xray-web-console

VPN-клиент `sing-box` (XRAY-совместимый) для тревел-роутера **GL.iNet MT3000 (Beryl)** с поведением, идентичным штатным WireGuard / OpenVPN-клиентам в прошивке GL.iNet — включая интеграцию с физическим переключателем на боку, killswitch и веб-панель управления.

Репозиторий содержит:
- runtime-конфиги, init-скрипты и hotplug-хуки для sing-box, разворачиваемые поверх стоковой прошивки GL.iNet **без её замены**;
- сборочные скрипты для статичного `sing-box` (musl/aarch64);
- веб-панель `xray-panel-cli` — Go single-binary с embed-UI для управления профилями, килсвитчем, биндом к переключателю, лайв-мониторингом трафика и логами.

> Симметричный проект для серверной стороны тоннеля (на Flint 2): [`flint2-xray-web-console`](../flint2-xray-web-console).

---

## Что есть

**На роутере:**
- `sing-box 1.13.x` (статичный musl-bin) с TUN-инбаундом + VLESS+Reality+Vision аутбаундом
- procd init `/etc/init.d/sing-box` с командами `killswitch_on/off`, `bind_switch_on/off`, `*_status`
- hotplug `/etc/hotplug.d/button/50-sing-box-switch` — sing-box следует физ. переключателю на корпусе, не мешая штатной WG/OVPN-логике GL.iNet
- веб-панель `xray-panel-cli` на `http://<lan-ip>:9092/`

**В веб-панели:**
- Live-статус: процесс sing-box, sing-tun, физ. переключатель, флаги UCI, активный профиль
- Тумблеры killswitch и bind_switch
- Кнопки start / stop / restart / reload sing-box
- CRUD профилей VLESS: импорт `vless://` URL → активация → автоматический ребилд `config.json` + `sing-box check` + reload + сброс активных connections
- Live-мониторинг: exit IP (через тоннель), текущая скорость up/down, top-10 активных потоков (host, network, объёмы)
- Tail логов sing-box с auto-scroll и auto-refresh

---

## Архитектура

### Дата-плоскость (sing-box)

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
| Физ. переключатель OFF + bind_switch=on | `stop_service` всё снимает → прямой WAN (или kill, см. таблицу ниже) |

### Контрол-плоскость (xray-panel-cli)

```
browser (LAN)  →  http://<lan-ip>:9092/  →  bcrypt basic-auth
                                              ↓
                                     embedded HTML/CSS/JS (vanilla)
                                              ↓ AJAX
                            +─ /api/state    ←─ pgrep / ip link / gl_util.sh / uci
                            ├─ /api/profiles ←─ /etc/xray-panel-cli/profiles.json
                            ├─ /api/service  ─→ /etc/init.d/sing-box
                            ├─ /api/{kill,bind}_switch  ─→ uci + init extra-cmds
                            ├─ /api/live     ←─ clash-API on 127.0.0.1:9090 + exit-IP poller
                            └─ /api/logs     ←─ tail /var/log/sing-box.log
```

Все out-of-process вызовы (pgrep, uci, init.d, clash-API) обёрнуты единым cache + single-flight (1.5с TTL) — несколько вкладок браузера не множат нагрузку на роутер.

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
├── HANDOFF.md                              # исходная постановка (legacy, обновлена)
│
├── router/etc/                             # файлы, заливаемые на роутер
│   ├── sing-box/config.example.json        # шаблон с placeholders
│   ├── config/sing-box                     # UCI defaults (enabled/killswitch/bind_switch/active_profile)
│   ├── init.d/sing-box                     # procd init с extra-commands
│   └── hotplug.d/button/50-sing-box-switch
│
├── cmd/xray-panel-cli/main.go              # точка входа Go-панели
│
├── internal/                               # код панели
│   ├── config/         # panel.yaml + валидация
│   ├── http/           # сервер, роуты, embedded UI, basic-auth, LAN-bind guard
│   │   └── web/        # index.html, style.css, app.js (embed.FS)
│   ├── service/        # обёртка /etc/init.d/sing-box
│   ├── ucitool/        # обёртка `uci get/set/commit`
│   ├── sysprobe/       # pgrep / ip link / gl_util.sh
│   ├── runner/         # абстракция exec для тестируемости (Fake + Exec)
│   ├── store/          # CRUD profiles.json
│   ├── vless/          # парсер vless:// URL
│   ├── singbox/        # render config.json из embed-template
│   ├── clash/          # REST-клиент clash-API
│   ├── exitip/         # фоновый поллер api.ipify.org
│   └── logs/           # эффективный backwards-seek tail
│
├── deploy/
│   ├── panel.example.yaml                  # шаблон конфига панели
│   ├── xray-panel-cli.init                 # procd init для панели
│   └── install.sh                          # cross-compile + scp + перезапуск
│
├── cmd/vless-vet/main.go                   # утилита: фильтр + сетевая проверка vless:// URL
│
└── scripts/
    ├── build-sing-box.sh                   # кросс-компиляция sing-box (musl-friendly)
    └── install.sh                          # деплой sing-box (без панели)
```

Реальные секреты (`router/etc/sing-box/config.json`, `deploy/panel.yaml`, `dist/`, `build/`) **в git не коммитятся**.

---

## Установка с нуля

### 0. Pre-flight на роутере (одноразово)

SSH на роутер (предполагается алиас `beryl` в `~/.ssh/config`, root-доступ):

```sh
ssh beryl
opkg update
opkg list-installed | grep -E 'kmod-tun|iptables-mod-tproxy|kmod-ipt-tproxy'
# если чего-то нет:
opkg install kmod-tun iptables-mod-tproxy kmod-ipt-tproxy
mkdir -p /etc/hotplug.d/button   # на стоковом MT3000 директория пустая
```

### 1. Сборка sing-box

На локальной машине нужен Go 1.22+. Бинарник статический, musl-совместимый — официальные релизы SagerNet линкуются с glibc и на OpenWrt не работают.

```sh
./scripts/build-sing-box.sh
# → build/sing-box-build/sing-box-static
```

### 2. Локальный sing-box-конфиг с твоими credentials

```sh
cp router/etc/sing-box/config.example.json router/etc/sing-box/config.json
# открыть config.json, подставить __VPN_HOSTNAME__, __VLESS_UUID__,
# __REALITY_SNI__, __REALITY_PUBLIC_KEY__, __REALITY_SHORT_ID__
```

> Если поставишь панель `xray-panel-cli` — этот шаг можно пропустить: панель сама нарисует профиль из `vless://` URL.

### 3. Деплой sing-box

```sh
./scripts/install.sh beryl
```

Заливает: бинарник в `/usr/bin/sing-box`, UCI в `/etc/config/sing-box`, init и hotplug, валидирует конфиг через `sing-box check`, включает и стартует сервис.

### 4. Сборка и деплой веб-панели `xray-panel-cli`

```sh
./deploy/install.sh beryl
```

Кросс-компилирует панель (`GOOS=linux GOARCH=arm64 CGO_ENABLED=0`, ~6 МБ), заливает бинарник + init + конфиг-шаблон, **попутно отключает MPTCP** (см. [гoтча](#ограничения-и-гoтча)), и (пере)стартует сервис.

При первой установке создаётся `/etc/xray-panel-cli/panel.yaml` из примера — нужно **поправить bcrypt-пароль** перед стартом:

```sh
ssh beryl
# сгенерируй хеш: htpasswd -nbBC 12 admin 'mypassword' | cut -d: -f2
vi /etc/xray-panel-cli/panel.yaml         # подставь password_bcrypt
/etc/init.d/xray-panel-cli enable
/etc/init.d/xray-panel-cli start
```

### 5. Проверка

```sh
# с роутера:
ssh beryl 'curl -sS https://api.ipify.org'
# должно выдать IP экзита Flint 2 (например, 176.221.192.204)

# с LAN-устройства (мак/телефон в Wi-Fi роутера):
curl https://ifconfig.me                        # тот же exit IP
open http://192.168.200.1:9092/                 # веб-панель
```

---

## CLI-команды управления

```sh
# стандартные procd
/etc/init.d/sing-box start | stop | restart | reload | enable | disable

# наши extra-commands (UCI-state переживает ребуты)
/etc/init.d/sing-box killswitch_on | killswitch_off | killswitch_status
/etc/init.d/sing-box bind_switch_on | bind_switch_off | bind_switch_status

# панель
/etc/init.d/xray-panel-cli start | stop | restart | reload | enable | disable
```

Альтернатива CLI — те же действия в веб-панели на `http://<lan-ip>:9092/`.

---

## Веб-панель (`xray-panel-cli`)

### Экраны

1. **Sing-box** — статус (Process / TUN / Boot / Active profile), кнопки управления.
2. **Profiles** — список VLESS-профилей с активным помечен ACTIVE, кнопки Activate / Delete, форма импорта `vless://` URL.
3. **Killswitch** — тумблер ON/OFF (синхронизирован с UCI `sing-box.config.killswitch`).
4. **Physical switch** — позиция переключателя на корпусе + тумблер bind_switch (UCI `sing-box.config.bind_switch`).
5. **Live** — exit IP, текущая скорость up/down, кол-во активных connections, top-10 потоков.
6. **Logs** — последние 200 строк `/var/log/sing-box.log` с auto-refresh (3с) и auto-scroll.

### REST API

| Метод | Путь | Что делает |
|---|---|---|
| GET  | `/api/ping` | health-check (под basic-auth) |
| GET  | `/api/state` | агрегированный статус (sing-box, tun, switch, killswitch, bind_switch, enabled, active_profile, sw_func, native_vpn_active) |
| POST | `/api/service` | `{action: start\|stop\|restart\|reload}` |
| POST | `/api/killswitch` | `{on: bool}` |
| POST | `/api/bind_switch` | `{on: bool}` (legacy; новый код использует `/api/side-switch`) |
| POST | `/api/side-switch` | `{on: bool}` — Phase 4: транзакционный своп native↔XRAY + бинд физ. переключателя |
| POST | `/api/native-vpn/stop` | дизейблит активные WG/OVPN-правила в `route_policy`, рестартит `vpn-client`, сохраняет список для restore |
| POST | `/api/native-vpn/restore` | возвращает ранее дизейбленные правила, рестартит `vpn-client` |
| GET  | `/api/launcher-config` | публичный (без auth) — отдаёт `{mode}` для launcher.js на стоковом UI |
| GET  | `/api/profiles` | список профилей + active_id |
| POST | `/api/profiles/import-vless` | `{url, name?}` — парсит и сохраняет |
| DELETE | `/api/profiles/{id}` | удалить (отказ если активный) |
| POST | `/api/profiles/{id}/activate` | render → sing-box check → write → uci commit → reload + clash close |
| GET  | `/api/live` | exit IP, traffic rates, top flows |
| GET  | `/api/logs?lines=N` | tail sing-box.log (default 100, max 1000) |

Под капотом — `/api/state` (3с) и `/api/live` (1.5с) имеют кеш с single-flight, чтобы N браузерных вкладок не множили нагрузку.

CORS: все `/api/*` отдают `Access-Control-Allow-Credentials: true` только для RFC1918 / loopback Origin'ов, чтобы launcher на `http://192.168.200.1/` мог дёргать панель на `:9092` cross-origin (см. [internal/http/cors.go](internal/http/cors.go)).

### Ресурсы

- ~6 МБ дисковое место (статичный бинарник)
- ~17 МБ RAM (RSS) при работе (Go-runtime, sing-box доп. 20-40 МБ когда запущен)
- Peak CPU 5–15% на пиках при нескольких вкладках; 1–3% средняя

### Интеграция со стоковым GL.iNet VPN Dashboard (Phase 2-4)

Помимо отдельной панели на `:9092`, `xray-panel-cli` отдаёт `xray-panel-launcher.js` который инжектится в стоковый GL.iNet UI (`/www/gl_home.html`). Launcher добавляет к стоковому Vue-SPA на `#/vpndashboard`:

- **XRAY карточку** рядом со стоковыми WireGuard/OpenVPN — в едином визуальном стиле
- **ON/OFF тоггл** в стиле `gl-switch` (с собственным overlay-текстом, т.к. Vue-state не пробросить)
- **Профиль-пикер** (drawer с радио-группой профилей VLESS)
- **Kill Switch** тег — клик → POST `/api/killswitch`
- **Side switch селектор** — широкий gl-switch-style тоггл с лейблами «WireGuard VPN» ↔ «XRAY VPN», транзакционный своп (стопает native, поднимает XRAY, биндит физ-кнопку; обратно — симметрично)
- **View Log** drawer — tail `/var/log/sing-box.log`
- Connected-state строки: Server / Port / Traffic / Virtual IP / Exit IP

Режим инъекции переключается в `/etc/xray-panel-cli/panel.yaml` (`injection.mode: dashboard | legacy | full`).

Полная техническая раскладка — в [HANDOFF-DASHBOARD-INTEGRATION.md](HANDOFF-DASHBOARD-INTEGRATION.md).

---

## Поведенческая матрица (как у GL.iNet WG/OVPN)

Три входных параметра в UCI (`/etc/config/sing-box`): `enabled`, `killswitch`, `bind_switch`. Плюс физический переключатель на корпусе. Дальше — как это всё взаимодействует.

### Таблица 1. Steady state — что видит LAN при текущих настройках

| # | enabled | killswitch | bind_switch | phys.switch | sing-box | LAN видит |
|---|---|---|---|---|---|---|
| 1 | 1 | 0 | 0 | — | running | через тоннель (exit IP сервера, например `176.221.192.204`) |
| 2 | 1 | 0 | 0 | — | stopped | **прямой WAN bypass** (carrier / home IP) |
| 3 | 1 | **1** | 0 | — | running | через тоннель; если тоннель упадёт → blackhole |
| 4 | 1 | **1** | 0 | — | stopped | **blackhole** (killswitch активен независимо от sing-box) |
| 5 | 1 | 0 | **1** | ON | running | через тоннель |
| 6 | 1 | 0 | **1** | OFF | stopped | прямой WAN bypass |
| 7 | 1 | **1** | **1** | ON | running | через тоннель; падение → blackhole |
| 8 | 1 | **1** | **1** | OFF | stopped | **blackhole** |
| 9 | 0 | * | * | * | (не стартует на boot) | прямой WAN bypass или blackhole — по killswitch |

«—» = неважно. * = неважно. **Правило:** killswitch — независимый toggle. Если он ON, LAN дропается **всегда, когда тоннель не работает**, неважно почему (manual stop / sing-box упал / bind+phys=OFF).

### Таблица 2. Действия — что произойдёт при нажатии

| Действие | Контекст | Эффект на sing-box | Эффект на LAN правила |
|---|---|---|---|
| **Start** | bind_switch=0 | стартует | `prio 5000` → tun |
| **Start** | bind_switch=1, phys=ON | стартует | `prio 5000` → tun |
| **Start** | bind_switch=1, phys=**OFF** | **отказ 409** «physical switch is OFF» | без изменений |
| **Stop** | любой | останавливается | `prio 5000` снимается; killswitch (5500) — по UCI |
| **Restart / Reload** | те же гарды что и Start | stop + start | то же |
| **Killswitch ON** | любой | без изменений | `prio 5500 blackhole iif br-lan` ставится сразу |
| **Killswitch OFF** | любой | без изменений | `prio 5500` снимается |
| **Bind switch ON** | phys=ON | если sing-box stopped → стартует | `prio 5000` появится |
| **Bind switch ON** | phys=OFF | если sing-box running → останавливается | `prio 5000` снимается |
| **Bind switch OFF** | любой | без изменений | без изменений |
| **Activate profile** | sing-box running | render → check → write → reload + DELETE /connections | без изменений |
| **Activate profile** | sing-box stopped | render → check → write (без reload) | без изменений |

### Таблица 3. Физический переключатель (hotplug)

| Событие | bind_switch | sw_func GL.iNet ≠ vpn/wireguard/... | Что hotplug делает |
|---|---|---|---|
| Flip → ON  (`pressed`) | 1 | да (свободен) | `/etc/init.d/sing-box start` |
| Flip → OFF (`released`) | 1 | да (свободен) | `/etc/init.d/sing-box stop` + снять `prio 5500` |
| Flip любой | 0 | — | пропускает (выход 0) |
| Flip любой | 1 | привязан к WG/OVPN/Tor/AdGuard/Wi-Fi | пропускает (логирует, не вмешивается) |

Сценарий «kill всё-таки активен» при flip→OFF: hotplug **снимает** `prio 5500` принудительно, потому что физический OFF трактуется как «хочу обычный роутер». То есть **физический переключатель в OFF превалирует над UCI killswitch=1**. Сделано чтобы не подвешивать LAN, когда юзер физически выключил VPN.

### Таблица 4. Boot-time (после ребута роутера)

| UCI enabled | bind_switch | phys.switch | killswitch | После загрузки |
|---|---|---|---|---|
| 1 | 0 | — | * | sing-box стартует автоматически (S99). LAN → tun. Killswitch как настроен. |
| 1 | 1 | ON | * | sing-box стартует. LAN → tun. |
| 1 | 1 | OFF | 0 | sing-box не стартует (gated). LAN → прямой WAN. |
| 1 | 1 | OFF | 1 | sing-box не стартует. LAN → blackhole (killswitch). |
| 0 | — | — | * | sing-box не стартует. LAN → прямой WAN или blackhole — по killswitch. |

### Что считается "LAN"

Всё, что входит на роутер через `iif br-lan`. На GL.iNet MT3000 это и проводной LAN, и Wi-Fi 2.4/5 ГГц (бриджуются в `br-lan` стоковой прошивкой). Guest-сеть (`br-guest`), wgserver / ovpnserver bridges — **не покрываются**, добавлять явно.

### Уплинк

`auto_detect_interface: true` — sing-box подхватывает default route автоматически. Подключил роутер к Wi-Fi отеля / переткнул USB-tethering / воткнул ethernet — тоннель сам переустановится через новый WAN.

---

## Killswitch — детали (как у GL.iNet и как у нас)

### Stock GL.iNet killswitch

Три слоя инфраструктуры, работающие вместе:

**1. UCI декларация — `/etc/config/route_policy`**

Каждый WG/OVPN-клиент описан правилом с полями `via_type`, `via` (имя интерфейса), `mark` (свой fwmark, например `0x1000`), `killswitch` (та самая галка «Block non-VPN traffic» в UI), `enabled`.

**2. Маркировка — `iptables -t mangle PREROUTING` chain `ROUTE_POLICY`**

Сначала `connmark restore` (для ESTABLISHED соединений берётся прежний mark), потом каждое включённое route_policy правило ставит свой mark (`0x1000` для VPN), в конце `TUNNEL100_ROUTE_POLICY` ставит `0x8000` («novpn → main») всему, что не сматчилось.

**3. Маршрутизация — `ip rule` с пометкой `gl_vpn_rules` в `/etc/config/network`**

```
prio 6000  fwmark 0x8000/0xf000           lookup main          # novpn → main → WAN
prio 9000  not fwmark 0/0xf000            lookup main          # mark != 0 → main fallback
prio 9910  not fwmark 0/0xf000            blackhole            # mark != 0 и нет маршрута → kill
prio 9920  iif br-lan                     blackhole            # ultimate fail-safe для LAN
```

Когда VPN-клиент `wgclient1` поднимается, его network-скрипт **дополнительно** добавляет `prio ~7000 fwmark 0x1000/0xf000 lookup <wg-table>` и в эту таблицу — `default via <wg-peer> dev wgclient1`. Если `killswitch=1`, ставится ещё `prio ~9100 fwmark 0x1000 blackhole` РАНЬШЕ rule prio 9000 (lookup main fallback).

### Сценарий «killswitch ON, VPN ещё не подключился»

- Интерфейс `wgclient1` ещё не поднят (VPN-клиент в процессе хендшейка), `<wg-table>` пуста.
- LAN-пакет получает mark `0x1000` (по route_policy rule).
- `ip rule fwmark 0x1000 → wg-table` сматчился, таблица пуста → fallthrough.
- При `killswitch=1`: ловит `prio 9100 fwmark 0x1000 blackhole` → пакет дропается.
- При `killswitch=0`: fallthrough до `prio 9000 lookup main` → пакет уходит через WAN (утечка).

То есть GL.iNet killswitch — **отдельное blackhole-правило**, которое стоит с момента включения route_policy rule и пока VPN не установит соединение, фактически дропая весь трафик в это окно.

### Наш sing-box killswitch

Тот же паттерн, но проще, потому что у нас один профиль и нет необходимости в per-tunnel fwmark схемах:

```
prio 5000  iif br-lan  lookup 2022      # LAN → table 2022 (default = sing-tun)
prio 5500  iif br-lan  blackhole         # killswitch (если UCI killswitch=1)
```

Когда `sing-box` падает / только запускается / ещё не установил VLESS-сессию:
- `sing-tun` либо отсутствует, либо есть, но в `table 2022` нет default (sing-box чистит при stop).
- `prio 5000` сматчился → table пустая → **fallthrough**.
- Если есть `prio 5500` → blackhole → пакет дропается.
- Если нет → fallthrough до GL.iNet'овского `prio 6000 fwmark 0x8000 lookup main` → main → WAN bypass.

**Поведение полностью идентично GL.iNet'у**, включая «killswitch включён, VPN ещё не поднялся → LAN молчит».

### Отличия от GL.iNet

- **Match по `iif br-lan`**, не по fwmark. Не маркируем сами LAN-трафик — просто перехватываем по интерфейсу. Проще, не конфликтует с `TUNNEL100_ROUTE_POLICY`, который продолжает ставить `0x8000` параллельно (для tun-routed трафика mark становится «no-op»).
- **Физ. переключатель в OFF** при `bind_switch=1` принудительно снимает `prio 5500`, чтобы LAN не висел даже при UCI `killswitch=1`. У GL.iNet физ. переключатель отключает route_policy rule целиком — эффект тот же (нет VPN-rule → нет blackhole).
- **Не используем `route_policy`** — он жёстко завязан на их WG/OVPN инфраструктуру. Мы рядом, не вместо.

---

## Файлы на роутере

| Путь | Назначение |
|---|---|
| `/usr/bin/sing-box` | бинарник sing-box, ~35 МБ, статический |
| `/usr/bin/xray-panel-cli` | бинарник веб-панели, ~6 МБ, статический |
| `/etc/sing-box/config.json` | runtime-конфиг sing-box (DNS / TUN / VLESS+Reality / route) |
| `/etc/config/sing-box` | UCI: `enabled`, `killswitch`, `bind_switch`, `active_profile` |
| `/etc/init.d/sing-box` | procd init с extra-commands |
| `/etc/init.d/xray-panel-cli` | procd init для панели |
| `/etc/hotplug.d/button/50-sing-box-switch` | hook на физ. переключатель |
| `/etc/xray-panel-cli/panel.yaml` | конфиг панели (listen, paths, bcrypt creds) |
| `/etc/xray-panel-cli/profiles.json` | сохранённые VLESS-профили |
| `/etc/sysctl.d/99-disable-mptcp.conf` | отключение сломанного MPTCP в kernel |
| `/var/log/sing-box.log` | логи sing-box (tmpfs, очищается при ребуте) |

---

## Диагностика

```sh
# процессы
pgrep sing-box
pgrep xray-panel-cli

# логи
tail -f /var/log/sing-box.log
logread -e sing-box-switch          # события физ. переключателя
logread -e xray-panel-cli           # стартап / ошибки панели

# таблицы маршрутов
ip rule list
ip route list table 2022             # таблица sing-box (default → sing-tun)
ip route list table main

# firewall
iptables -L FORWARD -n -v --line-numbers | head -10

# clash-API напрямую (то же, что использует /api/live в панели)
curl -s http://127.0.0.1:9090/version
curl -s http://127.0.0.1:9090/connections | head -c 800

# UCI-состояние
uci show sing-box
```

---

## Утилита `vless-vet` — массовая проверка списка `vless://` URL

Когда натыкаешься на список из десятков-сотен публичных VLESS-конфигов
(репозитории на GitHub, телеграм-каналы и т. п.) — пропускаешь его через
`vless-vet`, чтобы:

1. отбросить URL, которые панель в принципе не сможет импортировать
   (неподдерживаемые transport / security, битый формат, и т. п.);
2. для оставшихся — проверить достижимость хоста (TCP connect → TLS
   handshake с правильным SNI);
3. **сгруппировать живые URL по комбинации `transport+security`**
   (`tcp+reality`, `tcp+tls`, `ws+tls`, …) — каждая группа в отдельной
   секции выходного файла, в порядке предпочтения. Так удобно сначала
   попробовать канонический `tcp+reality`, а потом по убыванию.

### Запуск

```sh
# базовый прогон: на вход — файл с одним vless:// на строку (комменты #
# допустимы). Результат — рядом, с ".alive" в имени.
go run ./cmd/vless-vet -in samples/raw.txt
# → samples/raw.alive.txt

# свой выходной путь
go run ./cmd/vless-vet -in samples/raw.txt -out /tmp/alive.txt

# больше параллельных проверок и более строгие таймауты
go run ./cmd/vless-vet -in samples/raw.txt -workers 128 \
    -tcp-timeout 2s -tls-timeout 3s

# только TCP-достижимость (без TLS-хендшейка) — быстрее, но слабее сигнал
go run ./cmd/vless-vet -in samples/raw.txt -skip-tls
```

### Источники: локальный файл, URL или preset (`-in` / `-url` / `-source`)

Можно скармливать на вход не только локальный файл — флаги `-source`
(преднастроенные публичные списки) и `-url` (произвольные URL'ы)
позволяют качать ленты прямо из утилиты. Любая комбинация флагов
**склеивается перед парсингом**:

```sh
# полный список kort0881 (clean/vless.txt — все регионы)
go run ./cmd/vless-vet -source kort0881

# только RU-SNI подсет (ru-sni/vless.txt — серверы с RU-доменами в SNI)
go run ./cmd/vless-vet -source kort0881-ru

# обе ленты разом
go run ./cmd/vless-vet -source kort0881-all
# то же через запятую: -source kort0881,kort0881-ru

# произвольный URL (например своя гист-лента)
go run ./cmd/vless-vet -url https://example.com/my-list.txt

# смешать локальный файл с удалённым источником
go run ./cmd/vless-vet -in samples/private.txt -source kort0881-ru

# тонкая настройка фетча
go run ./cmd/vless-vet -source kort0881 -fetch-timeout 60s
```

Известные preset'ы (вывод `-h` показывает свежий список):

| Preset | URL |
|---|---|
| `kort0881` | `…/kort0881/vpn-vless-configs-russia/main/githubmirror/clean/vless.txt` |
| `kort0881-ru` | `…/main/githubmirror/ru-sni/vless.txt` |
| `kort0881-all` | оба сразу |

Когда `-out` не задан, выходной файл получает имя на основе **primary
source**: `kort0881.alive.txt` для preset'ов, `<in>.alive<ext>` для
локального файла, `vless-vet.alive.txt` для ad-hoc URL.

### Фильтр по группам (`-only`)

Через запятую перечисляешь, какие бакеты записать в выходной файл.
Парсятся всегда все, проверяются по сети тоже все (чтобы статистика
в шапке отчёта была честной), но в файл попадают только нужные:

```sh
# только канонический бакет
go run ./cmd/vless-vet -in samples/raw.txt -only tcp+reality

# WebSocket+TLS и TCP+TLS
go run ./cmd/vless-vet -in samples/raw.txt -only "ws+tls,tcp+tls"

# любая security на TCP
go run ./cmd/vless-vet -in samples/raw.txt -only "tcp+*"

# Reality на любом транспорте
go run ./cmd/vless-vet -in samples/raw.txt -only "*+reality"
```

В консоли в конце прогона — breakdown живых URL по бакетам. Бакеты,
отброшенные `-only`, помечены `-` (они есть в живых, но в файл не
попадут):

```
DONE in 18s — kept 14/47 (tcp_ok=29 tls_ok=22) → samples/raw.alive.txt
    tcp+reality    11
  - tcp+tls         5
    ws+tls          3
```

В самом выходном файле каждая секция предварена заголовком — копируй
нужный кусок целиком:

```
# ── tcp+reality (11) ──
vless://...
vless://...

# ── ws+tls (3) ──
vless://...
```

> Прохождение TLS-хендшейка — сильный сигнал «хост жив и сконфигурирован
> под заявленную TLS/Reality-схему», но не гарантия успешной VLESS-сессии
> (особенно для Reality, где `tls_ok` означает «сервер ответил
> прикрытым cert'ом», а реальный VLESS-handshake идёт уже после).
> Чтобы отсеять и такие битые линки — используй `-deep` (см. ниже).

### Deep-проверка через локальный sing-box (`-deep`)

`tls_ok` мимикрирует под жизнь профиля — но ловит только «сервер
вообще отвечает». Классическое расхождение: TLS handshake проходит
(Reality честно отдаёт cert прикрытого сайта), а внутренняя VLESS-сессия
ломается из-за устаревших `uuid` / `pbk` / `sid`. На UI это
отображается как «тоннель UP, exit IP — закешированный, страницы не
открываются» — именно тот сценарий, который мы поймали в первой
итерации тестирования.

`-deep` поднимает на каждый TLS-passer **одноразовый локальный
sing-box** с минимальным конфигом: `socks-in` на эфемерном порту →
один VLESS-outbound (тестируемый профиль), затем делает HTTP/1.0 GET
на `http://www.gstatic.com/generate_204` через этот SOCKS. Если в
ответ прилетел 204 — пост-handshake VLESS реально несёт трафик,
профиль зачётный. Иначе — нет. В выходной файл при `-deep` попадают
**только** профили, прошедшие эту проверку.

```sh
# на macOS (нужен sing-box локально, поставь brew install sing-box):
go run ./cmd/vless-vet -in samples/raw.txt -deep

# с явным путём к бинарнику:
go run ./cmd/vless-vet -in samples/raw.txt -deep -singbox /opt/homebrew/bin/sing-box

# тюнинг concurrency и таймаута на профиль:
go run ./cmd/vless-vet -in samples/raw.txt -deep -deep-workers 8 -deep-timeout 12s

# на самом роутере (sing-box уже там), куда удобно скармливать большие списки:
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/vless-vet ./cmd/vless-vet
scp -O /tmp/vless-vet beryl:/tmp/   # busybox dropbear требует -O
ssh beryl '/tmp/vless-vet -in /tmp/raw.txt -deep -singbox /usr/bin/sing-box -deep-workers 4'
```

`-deep` несовместим с `-skip-tls` (deep сам опирается на TLS как
дешёвый предфильтр; если TLS не прошёл — VLESS-сессия гарантированно
не пройдёт, тестировать смысла нет).

Каждая deep-проверка — это запуск процесса sing-box, поэтому она
**медленнее** TCP/TLS-стадии: 2–10 секунд на профиль. Поэтому
`-deep-workers` по умолчанию 4 (а не 64, как у TCP-стадии). Для
списка из ~30 профилей с дефолтами — около минуты.

В шапке выходного файла при `-deep` появляется новая строка:

```
# Probe stage
#   TCP connect succeeded : 30 / 30
#   TLS handshake succeeded : 28 / 30
#   VLESS session OK : 11 / 30  (HTTP/204 fetched through sing-box+SOCKS)
#   kept in this file : entries that passed VLESS session (deep)
```

И итог в консоли:
```
DONE in 47s — kept 11/30 (tcp_ok=30 tls_ok=28 vless_ok=11) → samples/raw.alive.txt
    tcp+reality    8
    ws+tls         3
```

---

## Ограничения и гoтча

- **Нет nftables**, только iptables-legacy. `auto_redirect` от sing-box не используется (требует nft).
- **Не покрывает guest network и VPN-server bridges** — только `br-lan`.
- **При смене WAN** (USB-tether / Wi-Fi-репитер) есть короткая (≤5 сек) дыра, пока sing-box переустанавливает соединение через новый интерфейс. `procd respawn 3600 5 5` поднимает sing-box если он упадёт.
- **DNS upstream от роутера** идёт через `direct` outbound в обход тоннеля (`ip_is_private` matches LAN-gateway). Если хочется DNS-over-VPN — поправь route-rules.
- **Bootstrap loop** при резолве `vpn.sys-lab.xyz`: решён правилом `default_domain_resolver: local-dns` + DNS-rule `domain → local-dns` + порядком route-rules (`ip_is_private` ВЫШЕ `hijack-dns`).
- **MPTCP в kernel** OpenWrt 21.02 GL.iNet build — **сломан** для произвольных listener'ов (`subflow_v4_init_req` не реализован → SYN-ACK кривой `SRC=0.0.0.0 DST=0.0.0.0`, уходит в `lo`, LAN-клиент таймаутит). По умолчанию включён (`net.mptcp.enabled=1`). `uhttpd` патчем GL.iNet'а отключает MPTCP на сокете, поэтому стоковый UI работает; Go-listener'ы — нет. **Фикс:** `echo net.mptcp.enabled=0 > /etc/sysctl.d/99-disable-mptcp.conf` (применяется автоматически в `deploy/install.sh`).
- **busybox `pgrep -x`** на этой сборке всегда возвращает «not found» даже когда процесс есть. Используем `pgrep` без флагов (default match по `/proc/PID/comm`).
- **`-O` для scp** — busybox dropbear не поддерживает sftp-server; macOS scp 9+ по умолчанию использует sftp. install.sh всегда передаёт `-O`.

---

## Roadmap

### Phase 1 — done
- [x] Кросс-компиляция статичного sing-box под musl/aarch64
- [x] TUN inbound + VLESS+Reality+Vision outbound
- [x] LAN routing через `iif br-lan` без правки штатных правил GL.iNet
- [x] Killswitch (UCI-флаг + GL.iNet'овский blackhole)
- [x] Привязка к физическому переключателю через hotplug
- [x] Идемпотентный init (start/stop/restart/reload)
- [x] Снос устаревших пакетов (`v2raya`, `xray-core`)

### Phase 2 — веб-консоль (отдельная панель на :9092)
- [x] **2A.** Скелет: Go single-binary, embed UI, bcrypt basic-auth, LAN-bind guard, procd init, deploy-script, `/api/ping`
- [x] **2B.** Service API: `/api/state`, `/api/service`, `/api/killswitch`, `/api/bind_switch`. UI с тумблерами, кнопками, auto-refresh.
- [x] **2C.** Профили: парсинг `vless://` URL, CRUD, активация → пересборка `config.json` → reload + clash close. UI с импортом/удалением/активацией.
- [x] **2D.** Live data + логи: `/api/live` (exit IP через фоновый поллер, traffic rate, top flows через clash-API), `/api/logs` (tail). UI карточки Live и Logs.

### Phase 3 — интеграция с GL.iNet stock VPN Dashboard — done
- [x] Launcher.js инжектится в `/www/gl_home.html` (cache-busted hash)
- [x] XRAY карточка на `#/vpndashboard` рядом со стоковой WG/OVPN
- [x] Клик на ON/OFF тоггл → `/api/service start|stop`
- [x] Killswitch тег с inline-кликом → `/api/killswitch`
- [x] Profile-picker drawer с радио-группой (свой DOM, не Element-UI)
- [x] Forward mutex: XRAY ON стопает native через `/api/native-vpn/stop`, XRAY OFF восстанавливает через `/api/native-vpn/restore`
- [x] Connected-state extras (Server / Port / Traffic / Virtual IP / Exit IP) + View Log drawer

### Phase 4 — Side switch + транзакционный своп — done
- [x] Side switch селектор (широкий gl-switch с лейблами "WireGuard VPN" / "XRAY VPN") рядом с Kill Switch
- [x] `POST /api/side-switch {on:bool}`: своп `switch-button.@main[0].func` `vpn` ↔ `xray`, бинд физ-кнопки к sing-box, синхронизация с текущей позицией переключателя
- [x] Симметричный mutex: ON стопает native (если запущен) перед стартом XRAY, OFF стопает XRAY и восстанавливает native — переключение в любую сторону оставляет систему в согласованном состоянии
- [x] Stock UCI ключ `switch-button.@main[0].func='xray'` обрабатывается нашим hotplug; стоковый `/etc/rc.button/switch` пытается выполнить несуществующий `/etc/gl-switch.d/xray.sh` → no-op без `mcu_send_message` нотификации
- [x] Backup-скрипт включает `/etc/config/switch-button` — состояние Phase 4 переживает rebuild прошивки

### Phase 5 — TODO
- [ ] Симметричный апдейт для серверной стороны (`flint2-xray-web-console`)
- [ ] Multi-outbound `selector` / `urltest` для real-time fail-over без рестарта sing-box
- [ ] WebSocket-стрим логов (вместо REST polling)
- [ ] Latency-test через clash-API в UI
- [ ] Edit/rename профилей (сейчас только Add/Activate/Delete)
- [ ] Опциональный i18n (английский по умолчанию, русский opt-in)
- [ ] Сплит launcher.js (~1950 строк) на модули (core / dashboard / drawer / log / side-switch)

---

## Стек панели (резюме)

- **Backend:** Go 1.25, single binary (~6 МБ статичный musl), embedded UI через `embed.FS`, basic-auth (bcrypt), LAN-bind guard.
- **Frontend:** vanilla HTML/CSS/JS (no build step), dark navy тема, mobile-friendly (44pt tap targets, safe-area, no double-tap zoom).
- **Порт:** `9092` (на beryl 9090 занят clash-API; 80/443/8080/8443 — nginx/uhttpd/GL.iNet UI).
- **Auth:** общий bcrypt-хеш с flint2-панелью — одни логин/пароль на обе консоли.
- **Конфиг:** `/etc/xray-panel-cli/panel.yaml` (LAN-bind, paths, creds, optional `exit_ip_url`).
- **Сборка:** `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/xray-panel-cli`
- **Деплой:** `./deploy/install.sh beryl`
