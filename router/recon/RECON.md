# RECON — stock VPN Dashboard integration (beryl, GL.iNet 4.8.1)

Цель: понять, во что инжектиться, чтобы XRAY-клиент жил в `#/vpndashboard`
рядом со штатными WireGuard / OpenVPN.

Артефакты (этот каталог):
- `spa/gl-sdk4-ui-vpndashboard.common.js` — Vue-бандл дашборда (228 KB)
- `spa/gl-sdk4-ui-btnsettings.common.js` — Vue-бандл "Button Settings" (45 KB)
- `spa/gl-sdk4-ui-wgclient.common.js` — Vue-бандл WG-клиента (139 KB)
- `spa/gl-sdk4-ui-ovpnclient.common.js` — Vue-бандл OVPN-клиента (131 KB)
- `spa/app.73f13df2.js` — корневой app (1.9 MB)
- `spa/gl_home.html`, `spa/index.html` — entry HTML
- `dom/` — место для DOM-снимков (TBD: ручной экспорт через DevTools)

---

## 1. Бэкенд: какие RPC дёргает дашборд

Все вызовы — через JSON-RPC обёртку `s("call", ["sid", "<object>", "<method>", payload])`.
"sid" = session-id; реальный канал — `/cgi-bin/api` → ubus поверх `rpcd`.

Перечислены все, найденные в бандле:

| Object | Method | Назначение |
|---|---|---|
| `vpn-client` | `get_status` | live-статус всех тоннелей: `{status_list: [...]}` |
| `vpn-client` | `get_tunnel` | список тоннелей (per-tunnel: id, name, type, killswitch, …) |
| `vpn-client` | `set_tunnel` | модификация тоннеля |
| `vpn-client` | `add_tunnel` | создать новый |
| `vpn-client` | `remove_tunnel` | удалить |
| `vpn-client` | `order_tunnel` | переупорядочить |
| `vpn-client` | `set_default_tunnel` | какой тоннель "по умолчанию" (активный) |
| `vpn-client` | `set_global_mode` | переключить **PROXY MODE** (GLOBAL vs Policy) — **не** тип VPN |
| `vpn-client` | `set_options` | `{tunnel_id, mtu, local_access, masq, service_policy, killswitch}` |
| `vpn-client` | `set_tap_s2s` | TAP site-to-site режим |
| `vpn-client` | `start_random_client` | поднять "случайный" клиент (auto-mode) |
| `vpn-client` | `get_all_config_list` | список конфигов по всем протоколам |
| `vpn-client` | `get_connection_methods` | какими способами можно подключиться |
| `vpn-client` | `check_domain_online` | резолв проверка |
| `wg-client` | `get_config_list` | per-protocol список WG-конфигов |
| `wg-client` | `get_group_list` | WG-провайдеры (Mullvad, NordVPN…) |
| `clients` | `get_list` | LAN-клиенты |

**Где найти**: `python3 -c "import re; s=open('spa/gl-sdk4-ui-vpndashboard.common.js').read(); [print(m) for m in re.findall(r'\\[\"sid\",\"[^\"]+\",\"[^\"]+\"', s)]"`

---

## 2. Бэкенд: UCI и шелл-handler'ы

### `/etc/config/route_policy` — главное хранилище тоннелей

Стоковая прошивка хранит ВСЕ VPN-клиенты как `config rule` записи в `route_policy`:

```
config rule
    option name 'Primary Tunnel'
    option tunnel_id '10'
    option enabled '0'          # ← включён ли (тоггл ON/OFF на дашборде)
    option killswitch '1'       # ← per-tunnel killswitch
    option via_type 'wireguard' # | 'openvpn' | 'novpn'
    option group_id '6509'
    option peer_id '3373'       # для WG: peer в /etc/config/wireguard
    option local_access '1'
    option via 'wgclient1'      # имя интерфейса
    option options_in_used '0'
```

Также есть `default`, `rule_process`, и т. п. — нам не интересны.

### `/etc/config/switch-button` — bind физ. переключателя

```
config main
    option func 'vpn'           # одна из: vpn / wireguard / openvpn / tor / adguardhome / wifi / repeater / cellular / off
    option sub_func '10'        # для func=vpn — это tunnel_id из route_policy
```

### `/etc/gl-switch.d/vpn.sh` — обработчик физ. переключателя

При flip:
1. Читает `switch-button.@main[0].sub_func` → tunnel_id
2. В `route_policy` находит rule с этим tunnel_id
3. `uci set route_policy.<rule>.enabled=<on|off>` + `uci commit`
4. `/etc/init.d/vpn-client restart`

Жёстко проверяет `via_type ∈ {wireguard, openvpn, novpn}` — иначе skip.
Значит, если мы добавим `via_type='xray'` запись в `route_policy`, физ. переключатель
её **не тронет** (нам это и нужно — наш hotplug `50-sing-box-switch` уже работает
параллельно).

### `/etc/init.d/vpn-client`

Сингл-демон, поднимающий wg/ovpn клиентов по `route_policy`. Не знает про xray.

### `ubus list`

`vpn-client` не светится как ubus-объект напрямую — он за rpcd ACL.
RPC поступают через `/cgi-bin/api`.

---

## 3. Frontend: Vue-структура дашборда

### Component hierarchy (вкратце)

```
VPN Dashboard (root, scoped data-v-1a3fa8b7)
├── header
│   ├── switch-mode-btn  → handleShowSwitchMode → opens <switch-mode> dialog
│   │                      (это и есть "Global Mode" dropdown справа сверху)
│   └── add-instance-btn (опц.)
├── tunnel list (v-for over this.tunnels)
│   ├── tunnel-card (per WG/OVPN tunnel)
│   │   ├── tunnel-icon-wrapper (scoped data-v-aadde574)
│   │   │   └── .killswitch-icon (отображается если tunnel.killswitch=true)
│   │   ├── kill-switch-tag → текст "Kill Switch" / "Failover"
│   │   ├── gl-dropdown (settings cog) → triggers options-setting-dialog
│   │   └── status pill, profile name, ON/OFF toggle
│   └── …
├── switch-mode dialog (ref="switchMode")
│   └── values: PROXY_MODE.GLOBAL, PROXY_MODE.POLICY (не "wireguard/openvpn"!)
├── options-setting-dialog
│   └── settingForm: {killswitch, mtu, local_access, masq, service_policy, client_to_client}
└── wg-client-guide / single-mode (доп. виджеты)
```

**Важное открытие:** `switch-mode` dialog — это переключение между GLOBAL и Policy
**режимами роутинга**, а НЕ выбор протокола (wireguard/openvpn). Тип протокола
живёт per-tunnel в `route_policy.<rule>.via_type`. Это меняет план — нет смысла
вставлять "XRAY" в этот dropdown.

### Где якориться для инжекта XRAY

Каждый tunnel-card рендерится из `this.tunnels[i]`. У dashboard есть `tunnel-list-wrapper`
(scoped class), которая содержит все карточки. Наш XRAY-tunnel — это **синтетическая
карточка**, которую мы инжектим в конец этого списка (или в начало).

Стиль клонируем из существующей карточки через ту же стратегию, что в
[xray-panel-launcher.js:67-113](../www/xray-panel-launcher.js#L67-L113) для sidebar:
найти узел по data-v-attribute, клонировать `outerHTML`, заменить лейблы и навесить
свои обработчики.

### Per-tunnel data shape (по `set_options` payload)

```
{
  tunnel_id: number,
  killswitch: boolean,
  mtu: number,
  local_access: boolean,
  masq: boolean,
  service_policy: boolean,
  client_to_client: boolean        // только для wgserver
}
```

Наш аналог (`:9092/api/state` + новые endpoints):
```
{
  tunnel_id: "xray",               // marker, не конфликтует с числовыми ID
  killswitch: bool,                // ← наш sing-box.config.killswitch
  active_profile: {id, name, ...}, // ← из profiles store
  service: bool,                   // running/stopped
  enabled: bool                    // boot-autostart
}
```

---

## 4. Mapping: stock dashboard → наш XRAY-тоннель

| Стоковое действие | Что делает в стоке | Наш аналог |
|---|---|---|
| Кликнуть Global Mode dropdown | dialog `switch-mode` → `vpn-client.set_global_mode` | **Не трогаем.** GLOBAL/Policy режим у нас не имеет смысла. |
| Кликнуть settings cog у tunnel | `options-setting-dialog` → `vpn-client.set_options` | Наш drawer → `POST :9092/api/killswitch`, `POST :9092/api/profiles/{id}/activate` |
| Toggle Kill Switch в settings | поле `settingForm.killswitch` → set_options | Поле в нашем drawer → `POST :9092/api/killswitch` |
| Toggle ON/OFF | `route_policy.<rule>.enabled=1` + restart vpn-client | `POST :9092/api/service start\|stop` |
| Profile picker drawer | `vpn-client.get_all_config_list` + `set_default_tunnel` | Наш drawer → `GET :9092/api/profiles` + `POST :9092/api/profiles/{id}/activate` |
| Mutual exclusion (стоковая логика) | один `enabled=1` rule, остальные `enabled=0` | При XRAY ON: вызываем `vpn-client.set_global_mode({mode:"policy"})` + `vpn-client.set_default_tunnel({tunnel_id:0})` для остановки native; при native ON: наш sing-box stop через `:9092/api/service` |

---

## 5. Mutation observer для дашборда — чем отличается от sidebar

Текущий `xray-panel-launcher.js` инжектит:
1. Sidebar entry "VPN XRAY" (поверх Element-UI submenu)
2. Recolor topology icon на home page
3. Sidebar dot indicators

Для дашборда нужно:
1. Подгрузка только когда `location.hash === '#/vpndashboard'`
2. Watch для появления `.tunnel-list-wrapper` (или эквивалента)
3. Клонировать существующую `tunnel-card`, заполнить XRAY-данными
4. Перехватить клики на нашей карточке — `event.stopImmediatePropagation()` на capture phase, чтобы Vue-роутер не пытался открыть нашу карточку как нативный tunnel-detail
5. Settings drawer: клонировать `options-setting-dialog`, заполнить нашими полями
6. Periodic refresh: poll `/api/state` каждые 5с, обновлять статус-pill

**Риск:** SPA может ре-рендерить весь список при `getTunnelList()` calls после
любого set_tunnel/set_options. Идемпотентный re-attach как в текущем launcher.

---

## 6. Открытые вопросы / TODO для Phase 3

- [x] DOM-снимок `.vpn-dashboard-wrapper` — `dom/dashboard.html` (Получили
      бонусом и drawer `setting-drawer` — лениво ленится только
      `options-setting-dialog`).
- [ ] **Settings drawer (Phase 3)**: открыть стоковую шестерёнку у WG-тоннеля,
      сохранить `.options-setting-dialog` (когда `display: ""`) в
      `dom/options-dialog.html`. Только она нужна — `__body` лениво рендерится
      Vue, в нашем dashboard.html там пусто.
- [ ] Подтвердить, что `vpn-client.set_default_tunnel({tunnel_id:0})` действительно
      выключает все native клиенты — для mutual-exclusion (выбор XRAY → stop WG).
      Проверить через curl `/cgi-bin/api` с валидной sid.

- [ ] **Тест RPC**: проверить вручную через curl `/cgi-bin/api` что:
      - `vpn-client.set_default_tunnel({tunnel_id: 0})` действительно
        останавливает все native tunnels (для нашего mutex-режима)
      - `vpn-client.get_status` отдаёт что-то для опроса

- [ ] **Совместимость**: при включении нашего XRAY мы должны также
      `/etc/init.d/vpn-client stop`? Или достаточно `set_default_tunnel({0})`?

---

## 7. Phase 1+2 — что реализовано

**Backend** (`internal/`):
- `config/config.go`: добавлено поле `injection.mode` (`legacy | dashboard | full`,
  default `legacy`). Метод `InjectionMode()` нормализует.
- `http/cors.go`: новая middleware `corsLAN` — пропускает только RFC1918 / loopback
  origin'ы; preflight OPTIONS отвечается до auth.
- `http/launcher_config.go`: новый `GET /api/launcher-config`, публичный (без
  basic-auth), отдаёт `{mode}`.
- `http/server.go`: `/api/launcher-config` обрабатывается до auth-обёртки,
  как `/api/up.png`. CORS оборачивает весь mux.

**Frontend** (`router/www/xray-panel-launcher.js`):
- `fetchMode()` при загрузке тянет `/api/launcher-config`; fallback `legacy`
  при любой ошибке (≤3.5с timeout).
- `activateDashboardModule()` — гейт по mode + hash-route.
- `renderDashCard()` — клонирует стоковую `.gl-card-wrapper.single-mode-card`,
  переписывает label на "XRAY (sing-box)", тип на "VLESS+Reality", вешает
  click-handler'ы (capture phase + stopImmediatePropagation) на settings cog
  (open :9092), file-info (open XRAY drawer), gl-switch (POST /api/service).
- `buildXrayDrawer()` + `openXrayDrawer()` — клонируется стоковая
  `.setting-drawer`; список профилей — из `/api/profiles`; Apply → POST
  `/api/profiles/{id}/activate`.
- Polling `/api/state` каждые 5 сек на dashboard-route; обновляет статус
  карточки + текущий профиль.
- `hashchange` listener — авто-mount/unmount при навигации между страницами SPA.
- MutationObserver: если SPA перерисовала список, наша карточка
  переинжектится.

**Deploy / panel.example.yaml**:
- Добавлен блок `injection.mode: legacy` с комментариями.
- Существующие установки **не** получают этот блок автоматически (install.sh
  трогает panel.yaml только при первой установке). Чтобы протестировать:
  ```sh
  ./deploy/install.sh beryl
  ssh beryl 'cat >>/etc/xray-panel-cli/panel.yaml <<EOF

  injection:
    mode: dashboard
  EOF'
  ssh beryl '/etc/init.d/xray-panel-cli restart'
  # → refresh browser at http://192.168.200.1/#/vpndashboard
  ```

## 8. Rollback

**Откат с `dashboard` → `legacy`** (быстрый, не теряет ничего):
```sh
ssh beryl 'sed -i "s/mode: dashboard/mode: legacy/" /etc/xray-panel-cli/panel.yaml && /etc/init.d/xray-panel-cli restart'
# → refresh браузера
```

**Полный аварийный откат** (отключает launcher вообще):
```sh
ssh beryl 'cp /www/gl_home.html.bak /www/gl_home.html && rm -f /www/xray-panel-launcher.js'
```

## 9. Phase 1 — старый план (deprecated, см. секцию 7)

**Скоуп**: единственный новый элемент — read-only "Kill Switch" status badge,
который показывает текущее состояние нашего UCI `sing-box.config.killswitch`,
поверх существующего стокового UI. **Без** изменения route_policy, без
mutation на стоковых tunnels, без drawer'ов. Чисто визуальная валидация, что
DOM-инжект в дашборд работает.

**Файлы:**
- `router/www/xray-panel-launcher.js` — расширить:
  - новая функция `renderDashboardBadge()`, активируется на `#/vpndashboard`
  - polls `:9092/api/state` каждые 5с, рисует badge с цветом по killswitch
  - badge вставляется в `.vpn-dashboard-wrapper` header'а как additional row,
    стиль — клон `.kill-switch-tag` из существующей карточки

**Бэк**: 0 изменений.

**Acceptance:**
- На дашборде появляется наш XRAY-badge рядом со стоковыми
- При `curl :9092/api/killswitch -d '{"on":true}'` цвет badge меняется в течение 5с
- При навигации с/на дашборд badge корректно появляется/исчезает (route observer)
- При `firmware upgrade simulation` (правка `gl_home.html`) фолбэк сохраняется —
  без launcher.js дашборд работает как раньше

**Feature flag (предусмотреть сразу):** в `panel.yaml`:
```yaml
injection:
  mode: legacy        # legacy | dashboard | full
```
- `legacy` = текущее поведение (sidebar + topology icon)
- `dashboard` = + наш badge на vpndashboard
- `full` = + drawer + ON/OFF (Phase 2-3)

Backend выбирает, какие куски launcher.js собирать в финальный скрипт
(или включает через JS-флаг на window).

**Rollback на любой стадии**: `ssh beryl 'cp /www/gl_home.html.bak /www/gl_home.html && rm -f /www/xray-panel-launcher.js'` → один рефреш браузера, всё как было.
