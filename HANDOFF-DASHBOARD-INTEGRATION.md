# Handoff — Dashboard integration (Phase 2-4)

> Sequel to [HANDOFF.md](HANDOFF.md) (sing-box install). This document
> covers the work to embed XRAY into the stock GL.iNet VPN Dashboard
> as a first-class VPN client alongside WireGuard / OpenVPN.
>
> Read alongside:
> - [README.md](README.md) — user-facing how-to / behavioural matrix
> - [router/recon/RECON.md](router/recon/RECON.md) — reverse-eng of stock SPA (RPC names, DOM anchors, UCI schema)
> - [router/recon/dom/](router/recon/dom/) — captured DOM snapshots of stock pages

---

## Where we are right now

**Phase 2 (panel + launcher basics)** — done, in production.

**Phase 3 (full dashboard integration)** — done, in production:
- XRAY tunnel card injected into `#/vpndashboard` alongside native WG/OVPN
- Killswitch toggle (clickable badge) on the card
- Profile picker drawer (built from scratch — Element-UI clone proved too fragile)
- ON/OFF toggle with progress banner + post-start health check
- Mutual exclusion **both directions** (XRAY ON stops WG; WG ON stops XRAY)
- Reversible mutex: stopping XRAY restores native WG rules we previously disabled
- Connected-state details (Server / Port / Traffic / Virtual IP / Exit IP)
- "View Log" modal tailing `/var/log/sing-box.log` via `/api/logs`
- Periodic-table-style XRAY icon (Xr / REALITY / 443 / TLS)
- Custom ON/OFF text overlay on toggle (native CSS Vue-driven; couldn't drive it externally)

**Phase 4 (physical side-switch binding)** — done, in production:
- Side-switch **selector** on XRAY card (a wider gl-switch-style toggle
  with full VPN-name labels: "WireGuard VPN" ↔ "XRAY VPN"), next to
  Kill Switch tag
- `POST /api/side-switch` swaps `switch-button.@main[0].func` between
  the saved stock value (`vpn` / `wireguard` / etc.) and `xray`, sets
  `sing-box.config.bind_switch=1`, syncs sing-box to current physical
  position. Stock `/etc/rc.button/switch` sees `func=xray`, tries to
  exec `/etc/gl-switch.d/xray.sh` which doesn't exist → exit 0; no
  native handler fires, no `mcu_send_message` notification leak.
- **Transactional mutex**: Side switch ON, if a native WG/OVPN tunnel
  is currently UP, calls `stopNativeVPN` (route_policy.enabled=0 +
  vpn-client restart) BEFORE starting sing-box. The disabled list is
  memo'd to `sing-box.config.native_vpn_disabled`. Side switch OFF, if
  that memo is non-empty, stops sing-box first (so our ip-rule prio
  5000 vacates) then calls `restoreNativeVPN` to put route_policy
  back. Net effect: Side ON/OFF is a one-click swap between two
  configured VPN clients, leaving the system in a coherent state in
  either direction.
- init.d/sing-box + hotplug recognise either `bind_switch=1` OR
  `switch-button.@main[0].func=xray` (Phase 4 sets both)

**Earlier Phase-4 attempt (rolled back to current design):** the first
cut zeroed `sub_func` while leaving `func='vpn'`. Two issues killed it:
(1) our own hotplug yielded on `func=vpn` regardless of bind_switch,
so the physical toggle never reached us; (2) `sub_func=0` didn't
reliably stick — stock SPA refresh paths sometimes restored it. The
`func=xray` design avoids both: hotplug's existing `xray)` arm covers
us, stock pieces gracefully degrade to no-op, and there's a single
small UCI key change.

**What we explicitly chose NOT to do** (and why):
- Inject XRAY into the stock btnsettings cascader (variant B). User
  rarely visits that page; the dashboard-side tag is a better fit and
  doesn't require Vue-store hacks on a second page.
- Patch the stock VPN Dashboard's "Global Mode" dropdown. That control
  is GLOBAL/Policy proxy mode, **not** a VPN-type selector — we
  misread it initially. See RECON.md §3.

---

## Architecture (cross-cutting)

```
GL.iNet stock SPA  (192.168.200.1:80)
    │
    │ <script src="/xray-panel-launcher.js?v=…">  (cache-busted by md5)
    ▼
xray-panel-launcher.js  (mutation-observer injection into Vue SPA)
    │
    │ XHR cross-origin with credentials   (CORS allow-credentials, RFC1918 origins only)
    ▼
xray-panel-cli on :9092
    │
    ├── /api/state, /api/profiles, /api/live, /api/logs, /api/service, /api/killswitch
    ├── /api/native-vpn/{stop,restore}  ← mutex
    ├── /api/side-switch                ← physical-switch binding (Phase 4)
    ├── /api/launcher-config            ← public; tells launcher which mode is on
    │
    └── shells out to:  uci, /etc/init.d/{sing-box,vpn-client},
                        ip rule, iptables, clash-API (sing-box's REST)
```

### Feature flag

`/etc/xray-panel-cli/panel.yaml` has:
```yaml
injection:
  mode: dashboard   # legacy | dashboard | full
```

- `legacy` — sidebar entry only (Phase 1 baseline behaviour)
- `dashboard` — + XRAY card on `#/vpndashboard` (Phase 3+4 features)
- `full` — alias for dashboard right now (placeholder for future phases)

Launcher reads it at page-load via `GET /api/launcher-config` (public,
no auth) and gates its modules accordingly. Switching modes:
```sh
ssh beryl 'sed -i "s/mode: dashboard/mode: legacy/" /etc/xray-panel-cli/panel.yaml && /etc/init.d/xray-panel-cli restart'
```

---

## Key files changed in this phase

### Backend (Go)

| File | What it does |
|------|--------------|
| `internal/config/config.go` | Added `Injection.Mode` field + `InjectionMode()` accessor |
| `internal/http/cors.go` | **NEW** — CORS middleware, only allows RFC1918 / loopback origins |
| `internal/http/launcher_config.go` | **NEW** — public `GET /api/launcher-config` |
| `internal/http/native_vpn.go` | **NEW** — `/api/native-vpn/stop` + `/restore` (mutex with persisted memo) |
| `internal/http/side_switch.go` | **NEW** — `/api/side-switch` (Phase 4 binding) |
| `internal/http/server.go` | Wire CORS + new routes + public-bypass for `/api/launcher-config` |
| `internal/http/state.go` | Added `native_vpn_active`, `sw_func` block fields |
| `internal/sysprobe/sysprobe.go` | Added `NativeVPNActive()` probe (scans `ip -o link show` for wgclient*/ovpnclient*) |
| `deploy/panel.example.yaml` | Documented `injection:` block |

### Frontend

| File | Size before / after | Notes |
|------|---|---|
| `router/www/xray-panel-launcher.js` | ~540 → ~1950 lines | All injection logic. Single IIFE. Search by section header (`// ── …` comments) |

### Router files

| File | Change |
|------|--------|
| `router/etc/init.d/sing-box` | `start_service` gate now triggers when `bind_switch=1` OR `switch-button.@main[0].func=xray` |
| `router/etc/hotplug.d/button/50-sing-box-switch` | Yields to native if `sw_func` is a native function; honours either signal otherwise |

### Recon artefacts (do not edit)

- `router/recon/spa/*` — un-gzipped GL.iNet SPA bundles for grep'ing
- `router/recon/dom/dashboard.html` — DOM snapshot of `#/vpndashboard`
- `router/recon/dom/btnsettings.html` — DOM snapshot of `#/btnsettings`
- `router/recon/RECON.md` — RPC table, UCI table, DOM anchors

---

## API endpoints added

All under :9092, all behind basic-auth except `/api/launcher-config`
and `/api/up.png`. All accept cross-origin XHR from RFC1918 LAN
origins with credentials.

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/launcher-config` | — | `{mode}` — public, used by injected launcher to gate modules |
| POST | `/api/native-vpn/stop` | — | `{ok, disabled:[…rule keys…]}` — saves list to `sing-box.config.native_vpn_disabled` |
| POST | `/api/native-vpn/restore` | — | `{ok, restored:[…]}` — re-enables previously-disabled rules + restarts vpn-client |
| POST | `/api/side-switch` | `{on:bool}` | `{ok, on, prev_sw_func, native_stop\|native_restore, init_output}` — transactional VPN swap (see internal/http/side_switch.go) |

GET `/api/state` now includes:
- `native_vpn_active` — true if any `wgclient*` / `ovpnclient*` iface exists
- `sw_func` — current value of `switch-button.@main[0].func`

GET `/api/live` already returned `exit_ip.value` as an OBJECT
`{ip, fetched_at, age_sec}` — launcher reads `.ip`. (Old bug: I tried
to use the whole value, got `[object Object]` rendered).

---

## UCI keys we read / write

### Ours (`/etc/config/sing-box`)
- `sing-box.config.enabled` — read by init
- `sing-box.config.killswitch` — clickable Kill Switch tag → POST `/api/killswitch`
- `sing-box.config.bind_switch` — Side switch selector → POST `/api/side-switch` (also legacy signal honoured by init's start gate)
- `sing-box.config.active_profile` — UUID of current VLESS profile
- `sing-box.config.native_vpn_disabled` — comma-joined route_policy section keys disabled by us; used by `restoreNativeVPN` on XRAY OFF or Side switch OFF
- `sing-box.config.prev_sw_func` — Phase 4: saved stock `func` value (`vpn` / `wireguard` / etc.) to restore on Side switch OFF
- `sing-box.config.prev_sub_func` — legacy from first Phase 4 cut; new code reads it on Side switch OFF for migration cleanup, then clears

### Stock GL.iNet (we touch via Phase 4)
- `switch-button.@main[0].func` — Phase 4 sets to `"xray"` when Side switch claimed, restores from `prev_sw_func` when released. Only `"xray"` or the previously-saved value are written; never anything else.
- `switch-button.@main[0].sub_func` — never set by current code; legacy-cleaned on Side switch OFF if `prev_sub_func` is non-empty
- `route_policy.@rule[N].enabled` — Phase 3 / Phase 4 mutex flips to 0 when XRAY ON (forward) or Side switch ON (transactional), back to 1 on restore

---

## Deploy

```sh
cd ~/Documents/projects/home-lab/beryl-xray-web-console
./deploy/install.sh beryl
```

This:
1. Cross-compiles `xray-panel-cli` for `linux/arm64 musl`
2. SCPs binary + init + launcher.js + sing-box init + hotplug → `/tmp/` on beryl
3. Copies into `/usr/bin/`, `/etc/init.d/`, `/www/`, `/etc/hotplug.d/button/`
4. Patches `/www/gl_home.html` to load launcher (cache-busted with md5 hash query)
5. First install: creates `/etc/xray-panel-cli/panel.yaml` from example
   — **on first install you must edit it** (set bcrypt password) before
   `/etc/init.d/xray-panel-cli enable && start`
6. Subsequent installs: restart panel only

After first install + your test, to switch to dashboard mode:
```sh
ssh beryl 'echo "
injection:
  mode: dashboard
" >> /etc/xray-panel-cli/panel.yaml && /etc/init.d/xray-panel-cli restart'
```

---

## Rollback

### Soft (keep code, disable injection)

```sh
ssh beryl 'sed -i "s/mode: dashboard/mode: legacy/" /etc/xray-panel-cli/panel.yaml && /etc/init.d/xray-panel-cli restart'
```

Browser refresh → only sidebar entry + topology icon remain. Dashboard
card / drawer / Phase 4 tag disappear.

### Hard (remove launcher entirely)

```sh
ssh beryl 'cp /www/gl_home.html.bak /www/gl_home.html && rm -f /www/xray-panel-launcher.js'
```

Stock UI back to pristine. `xray-panel-cli` still runs at :9092
standalone if you want to manage XRAY without dashboard integration.

### Phase 4 specific (un-claim physical switch)

The normal path is clicking the Side switch selector OFF in the UI —
that runs the full symmetric restore (stop sing-box, re-enable native
route_policy rules, restart vpn-client, restore stock `func`).

If the UI is unreachable and you need to un-claim from the shell:
```sh
ssh beryl '
saved_func=$(uci -q get sing-box.config.prev_sw_func)
saved_sub=$(uci -q get sing-box.config.prev_sub_func)   # legacy
disabled=$(uci -q get sing-box.config.native_vpn_disabled)

# Restore stock func (default to "vpn" if no snapshot)
uci set switch-button.@main[0].func="${saved_func:-vpn}"
[ -n "$saved_sub" ] && uci set switch-button.@main[0].sub_func="$saved_sub"

# Clear all our Phase 4 markers
uci set sing-box.config.bind_switch=0
uci set sing-box.config.prev_sw_func=""
uci set sing-box.config.prev_sub_func=""

# Re-enable any route_policy rules we disabled, then clear the memo
if [ -n "$disabled" ]; then
  for k in $(echo "$disabled" | tr "," " "); do
    uci set "${k}.enabled=1"
  done
  uci set sing-box.config.native_vpn_disabled=""
  uci commit route_policy
fi

uci commit switch-button
uci commit sing-box
/etc/init.d/sing-box stop
/etc/init.d/vpn-client restart
'
```

---

## Known issues / quirks

1. **Stock WG card lag** — after `/api/native-vpn/stop`, native WG card
   may visually still show ON for ~5–15 sec until GL.iNet's own polling
   refreshes. We dispatch synthetic `focus` + `visibilitychange` events
   to nudge it (`nudgeStockSPA()`) but if their Vue components don't
   listen, fallback is natural polling. Hard refresh = instant.

2. **WebRTC IP leak on whatismyip-style sites** — these test sites use
   JS WebRTC ICE / cached localStorage to detect IP; show home IP even
   when tunnel is up. Use `ifconfig.me`, `api.ipify.org`, `ipinfo.io/ip` —
   server-side detection, accurate. The tunnel works correctly; the
   test sites lie. (Cost me an hour of debugging.)

3. **ON/OFF text on the toggle** — Element-UI's `gl-switch` has TWO
   `.msg` spans whose classes Vue swaps reactively:
   first span `.on-msg` (hidden) ↔ `.is-on` (visible "ON"),
   second span `.off-msg` (hidden) ↔ `.is-off` (visible "OFF").
   Driving the prop from outside Vue is brittle, so we hide every
   `.msg` regardless of class (`querySelectorAll(".msg")` →
   `opacity:0 + visibility:hidden + font-size:0 !important`) and
   overlay our own `<span data-xray-sw-label>`. The first cut only
   matched `.on-msg/.is-off/.off-msg` and missed `.is-on`, leaving
   native "ON" text visible alongside our overlay — looked like a
   doubled letter. Also: positioning matters — our OFF label uses
   `right:5px` to match native's `left:51px translate(-100%)` in a
   56-px-wide gl-switch; `right:9px` (4px closer to the thumb) lets
   the thumb's drop shadow clip the leading "O".

4. **First-paint flash** — fixed but worth knowing: on initial card
   render before `/api/state` lands, we used to show
   "(no profile selected)" / OFF defaults briefly. Now we render in
   `Loading…` state until first state lands.

5. **bind_switch + phys=off blocks `start`** — by design from Phase 1.
   If the user toggles Side switch ON but the physical switch is in
   the OFF position, sing-box stays stopped. Our `POST /api/service`
   start surfaces this as a 409 error → red banner "Could not start:
   bind_switch=on and physical switch is OFF".

6. **`ip rule` cleanup race during stop** — `/etc/init.d/sing-box stop`
   removes rule prio 5000 in `stop_service` then procd kills the
   process. Usually <1s overlap. If LAN ping looks flakey for a
   couple seconds during XRAY OFF transitions, that's why.

7. **State cache TTL** — both `/api/state` (3s) and `/api/live` (1.5s)
   have server-side single-flight caches. Right after an action (ON/OFF,
   profile activate, killswitch toggle), the launcher polls with
   `force:true` but the cache may still serve a stale snapshot for ~1
   tick. Phase 3 post-start health check waits 2s before re-polling
   to cover this.

---

## Open / next items

### Bugs to revisit if reproduced

- **Phase 3d transient ping-loss during XRAY OFF**: user reported ~40s
  of ping timeouts when toggling XRAY OFF before WG came back. State
  on the router during the bug window was never captured; current
  theory is either killswitch UCI was stale or there was an `ip rule`
  race. The reversible-mutex Phase 4 work (Side switch + automatic
  restore on XRAY OFF) probably narrows the window; needs re-test.

### Features deferred

- Phase 4 variant B (cascader injection in btnsettings) — explicitly
  declined by user. The Side-switch tag on the dashboard is the
  chosen UX. Variant B remains as a fallback if user changes mind.
- Bandwidth / connection visualization beyond the simple traffic
  totals (sparkline of clash-API rates).
- Persistent log filter / search in View Log drawer (currently raw
  tail).
- "Profile import from vless://" in the dashboard drawer (currently
  only on the :9092 standalone panel).

### Code hygiene

- `xray-panel-launcher.js` is now ~1950 lines in a single IIFE.
  Split into modules (`core`, `dashboard`, `drawer`, `log`,
  `side-switch`) if continuing further. Each is independently
  loadable.

---

## Test plan (smoke)

For someone picking this up fresh, exercise the following with a LAN
client on a Mac at `http://192.168.200.1/`:

1. Navigate `#/vpndashboard`. Expect: stock WG card + our XRAY card
   below. XRAY card initially in OFF state.
2. Click XRAY ON. Expect: blue progress banner → green success banner;
   XRAY card flips to ON; **WG card** flips to OFF within 5–15 sec
   (native UI lag).
3. From router (ssh): `curl -sS https://api.ipify.org` → should return
   the active VLESS profile's exit IP (NOT home WAN IP).
4. Click XRAY profile row → drawer opens with profiles list. Pick a
   different one, click Apply. Banner: "Activating … (instant switch)".
   `ssh beryl 'curl -sS https://api.ipify.org'` should change.
5. Click Kill Switch tag → tag goes orange/filled. `ip rule list` on
   router should show `prio 5500 blackhole iif br-lan`.
6. Click Kill Switch tag again → tag goes grey/outline.
7. Click Side switch selector (slides to "XRAY VPN" side, blue).
   `uci get sing-box.config.bind_switch` = 1.
   `uci get switch-button.@main[0].func` = `xray`.
   `uci get sing-box.config.prev_sw_func` = original (e.g. `vpn`).
   If native WG was up, it stopped: `ip link show wgclient1` fails;
   `uci get sing-box.config.native_vpn_disabled` contains a rule key.
8. Flip the physical side switch on the router OFF → XRAY card should
   go to OFF state within ~1 sec (kernel logs show
   `sing-box-switch: physical switch action=released (sw_func=xray bind_switch=1)`).
9. Flip side switch ON → XRAY card to ON, exit IP via active profile.
10. Click Side switch selector again (slides back to "WireGuard VPN",
    gray). `uci get switch-button.@main[0].func` restored to original;
    `bind_switch=0`; sing-box stopped; wgclient1 back up; memo cleared.
11. Click XRAY OFF → green banner "XRAY stopped — native WG/OVPN
    restoring". WG card should flip ON within ~5 sec.
12. Hard refresh page (Cmd+Shift+R). Card should re-render correctly
    matching current state (no "Loading…" stuck).
13. **Symmetric mutex scenario**: with WG UP and physical=ON, click
    Side switch → WG stops, sing-box starts. Click Side switch again
    → sing-box stops, WG comes back. (Tests step 7+10 round-trip.)

---

## Useful diagnostic commands

```sh
# State summary
ssh beryl '
echo "=== procs ===";
pgrep sing-box && echo sing-box: UP || echo sing-box: DOWN
ip link show wgclient1 >/dev/null 2>&1 && echo WG: UP || echo WG: DOWN
ip link show sing-tun  >/dev/null 2>&1 && echo sing-tun: UP || echo sing-tun: DOWN

echo "=== ip rules (filter our additions) ==="
ip rule list | grep -E "5000|5500|6000|9920" | head

echo "=== UCI key state ==="
uci show sing-box | grep -v password
uci get switch-button.@main[0].func
uci get switch-button.@main[0].sub_func
uci get sing-box.config.prev_sw_func 2>/dev/null
uci get sing-box.config.native_vpn_disabled 2>/dev/null

echo "=== clash selector ==="
curl -s http://127.0.0.1:9090/proxies/proxy | head -c 200

echo "=== exit IP via sing-box ==="
curl -sS --max-time 6 https://api.ipify.org
'

# Tail panel log (Go server)
ssh beryl 'logread -e xray-panel-cli -f'

# Tail sing-box log
ssh beryl 'tail -f /var/log/sing-box.log'

# Hot-redeploy launcher only (no panel rebuild)
scp -O router/www/xray-panel-launcher.js beryl:/tmp/launcher-new.js && \
  ssh beryl '
    cp /tmp/launcher-new.js /www/xray-panel-launcher.js
    H=$(md5sum /www/xray-panel-launcher.js | cut -c1-10)
    sed -i "s|src=\"/xray-panel-launcher.js[^\"]*\"|src=\"/xray-panel-launcher.js?v=${H}\"|" /www/gl_home.html
    echo deployed: v=$H
  '
```

---

## Anti-gotcha list (things to NOT do)

- **Don't `git push` from this repo** without checking that
  `router/recon/spa/` and `router/recon/dom/` are gitignored — they're
  un-gzipped GL.iNet SPA bundles + DOM snapshots, GL.iNet's content,
  not ours to redistribute. `RECON.md` is our own notes and stays
  tracked.
- **Don't write arbitrary values to `switch-button.@main[0].func`**.
  Side switch ON writes `xray`; OFF writes back whatever was saved
  in `prev_sw_func` (typically `vpn`, `wireguard`, etc.). Never
  manufacture a value — only the captured snapshot or the literal
  `xray` are valid. Other writes corrupt the user's stock binding.
- **Don't delete `sing-box.config.prev_sw_func`** while Side switch
  is still ON — you'll lose the restore target and the user will
  have to re-pick their native tunnel in stock btnsettings.
- **Don't delete `sing-box.config.native_vpn_disabled`** while Side
  switch is ON or while XRAY started via the dashboard toggle — it
  holds the rule keys that the symmetric mutex restores when XRAY
  goes OFF or Side switch slides back to WireGuard.
- **Don't match only `.on-msg/.is-off/.off-msg`** on the gl-switch's
  `.msg` spans — Vue swaps them to `.is-on` / `.off-msg` etc. based
  on reactive state. Use `querySelectorAll(".msg")` and apply
  `opacity:0 + visibility:hidden + font-size:0 !important` to every
  match. Skipping `.is-on` leaves native "ON" text visible alongside
  our overlay.
- **Don't `cloneNode` an Element-UI drawer** for new UI. We tried;
  too much Vue-managed state inside. Build from scratch with simple
  CSS. (Cards we DO clone — they're mostly static structure once
  scrubbed of inherited connected-state rows.)
- **`/etc/init.d/vpn-client` has no `stop()` function.** The default
  `rc.common stop` is a no-op for it. The right way to stop native
  VPN clients is `uci set route_policy.@rule[N].enabled=0; uci commit
  route_policy; /etc/init.d/vpn-client restart`. The restart re-runs
  `rtp2.sh` which tears down disabled interfaces.

---

## How to resume from a fresh chat

Paste this preamble:

> Continue work on `beryl-xray-web-console` — embedding XRAY (sing-box)
> into the GL.iNet stock VPN Dashboard. Phase 2-4 deployed, in
> production (HANDOFF-DASHBOARD-INTEGRATION.md). Side switch selector
> on the XRAY card transactionally swaps WireGuard ↔ XRAY and binds the
> physical toggle. Router is `beryl` ssh alias. Deploy via
> `./deploy/install.sh beryl`. Hot-deploy launcher only via the snippet
> in handoff doc.

Then state what you're trying to do — fix bug X, add feature Y, etc.
