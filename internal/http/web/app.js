// xray-panel-cli — Phase 2D wiring:
//   - poll /api/state every 5s, render pills inside each card head
//   - toggles for killswitch / bind_switch (POST /api/{killswitch,bind_switch})
//   - start / stop / restart / reload buttons (POST /api/service)
//
// One section per concept (sing-box, killswitch, physical switch + bind),
// each grouping its status pills with the related controls.

(function () {
    const $  = (sel) => document.querySelector(sel);
    const $$ = (sel) => Array.from(document.querySelectorAll(sel));

    const cell      = (key) => $(`[data-key="${key}"]`);
    const toggleBtn = (name) => $(`button[data-toggle="${name}"]`);
    const actionResult = $("#action-result");

    function setPill(el, cls, text) {
        if (!el) return;
        el.className = "pill " + cls;
        el.textContent = text;
    }

    function renderBlock(el, block, mapper) {
        if (!el) return;
        if (!block) {
            setPill(el, "pill-muted", "—");
            return;
        }
        if (block.error) {
            setPill(el, "pill-warn", "error");
            el.title = block.error;
            return;
        }
        const m = mapper(block.value);
        setPill(el, m.cls, m.text);
        el.title = "";
    }

    function setActionResult(msg, ok) {
        actionResult.textContent = msg;
        actionResult.className = "action-result " + (ok ? "action-ok" : "action-bad");
    }

    function setToggleVisual(name, on) {
        const btn = toggleBtn(name);
        if (!btn) return;
        btn.dataset.state = on ? "on" : "off";
        btn.textContent = on ? "ON" : "OFF";
    }

    // updateServiceGating disables Start/Restart/Reload when bind_switch
    // is ON and the physical switch is OFF, since the backend will
    // refuse those actions with 409 anyway. Stop is never gated. The
    // bind hint swaps to a state-aware version explaining what's
    // blocking the buttons.
    function updateServiceGating(bindOn, physState) {
        const blocked = bindOn && physState === "off";
        const gated = ["start", "restart", "reload"];
        for (const act of gated) {
            const b = document.querySelector(`button[data-action="${act}"]`);
            if (!b) continue;
            b.disabled = blocked;
            b.title = blocked
                ? "Disabled: bind_switch is ON and the side switch is OFF — flip it ON, or turn off auto-follow"
                : "";
        }
        const hint = document.querySelector('[data-hint="bind"]');
        if (hint) {
            if (bindOn) {
                hint.textContent = physState === "on"
                    ? "Auto-follow is ON, side switch is ON — sing-box runs while the switch stays ON. Flip it OFF to stop the tunnel without touching the panel."
                    : physState === "off"
                    ? "Auto-follow is ON, side switch is OFF — sing-box stays stopped. Manual Start/Restart/Reload are blocked; flip the switch ON, or turn off auto-follow first."
                    : "Auto-follow is ON. The hardware switch position couldn't be read — falling back to manual mode.";
            } else {
                hint.textContent = "When on, sing-box follows the GL.iNet hardware mode switch on the side of the router (ON = start, OFF = stop), and the manual buttons above are gated on the switch position. Acts like the native WG / OVPN binding.";
            }
        }
    }

    function pad2(n) { return String(n).padStart(2, "0"); }

    function formatTime(iso) {
        if (!iso) return "—";
        const d = new Date(iso);
        if (isNaN(d.getTime())) return iso;
        return `${pad2(d.getDate())}.${pad2(d.getMonth() + 1)}.${d.getFullYear()} `
             + `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
    }

    async function fetchState() {
        try {
            const r = await fetch("/api/state", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const s = await r.json();

            renderBlock(cell("service"), s.service, (v) =>
                v ? { cls: "pill-ok", text: "running" } : { cls: "pill-bad", text: "stopped" });

            // Rerender the profile list when running flips, so the
            // active row's button switches between Stop (running) and
            // Activate (stopped). Skip on first load (lastRunning =
            // null) since fetchProfiles fires from init() anyway.
            const newRunning = !!(s.service && s.service.ok && s.service.value);
            if (lastRunning !== null && lastRunning !== newRunning) {
                fetchProfiles();
            }
            lastRunning = newRunning;
            renderBlock(cell("tun"), s.tun, (v) =>
                v ? { cls: "pill-ok", text: "up" } : { cls: "pill-bad", text: "down" });
            renderBlock(cell("enabled"), s.enabled, (v) =>
                v ? { cls: "pill-ok", text: "enabled" } : { cls: "pill-muted", text: "disabled" });

            renderBlock(cell("active_profile"), s.active_profile, (v) =>
                v && v.name ? { cls: "pill-ok", text: v.name } : { cls: "pill-muted", text: "none" });

            // killswitch_label is the inline text inside the
            // killswitch strip ("On" / "Off") — pairs with the toggle
            // button so the descriptive sentence reads naturally.
            const ksOn = !!(s.killswitch && s.killswitch.ok && s.killswitch.value);
            const ksLabel = document.querySelector('[data-key="killswitch_label"]');
            if (ksLabel) ksLabel.textContent = ksOn ? "On" : "Off";

            renderBlock(cell("physical_switch"), s.physical_switch, (v) =>
                v === "on"  ? { cls: "pill-ok",    text: "ON" } :
                v === "off" ? { cls: "pill-muted", text: "OFF" } :
                              { cls: "pill-warn",  text: v || "unknown" });

            $("#generated-at").textContent = "Updated: " + formatTime(s.generated_at);

            if (s.killswitch && s.killswitch.ok) setToggleVisual("killswitch", !!s.killswitch.value);
            if (s.bind_switch && s.bind_switch.ok) setToggleVisual("bind_switch", !!s.bind_switch.value);

            // Gate Start/Restart/Reload when bind=ON and the physical
            // switch is OFF — the panel's POST /api/service would
            // return 409 anyway (matching the init script's refusal),
            // and showing buttons disabled rather than letting the
            // user click into an error is friendlier. Stop is never
            // gated. The hint underneath the action row swaps to a
            // bind-aware version so the reason is obvious.
            const bindOn = !!(s.bind_switch && s.bind_switch.ok && s.bind_switch.value);
            const phys   = s.physical_switch && s.physical_switch.ok ? s.physical_switch.value : null;
            updateServiceGating(bindOn, phys);
        } catch (err) {
            setActionResult("Failed to load /api/state: " + err.message, false);
        }
    }

    async function postJSON(url, body) {
        const r = await fetch(url, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            credentials: "same-origin",
            body: JSON.stringify(body),
        });
        const data = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
        return data;
    }

    async function setBusy(btn, fn) {
        const before = btn.disabled;
        btn.disabled = true;
        try { await fn(); } finally { btn.disabled = before; }
    }

    function bindToggle(name, label) {
        const btn = toggleBtn(name);
        if (!btn) return;
        btn.addEventListener("click", () => setBusy(btn, async () => {
            const desired = btn.dataset.state !== "on";
            try {
                await postJSON("/api/" + name, { on: desired });
                setActionResult(label + " → " + (desired ? "ON" : "OFF"), true);
                await fetchState();
            } catch (err) {
                setActionResult(label + " failed: " + err.message, false);
            }
        }));
    }

    function bindAction(btn) {
        btn.addEventListener("click", () => setBusy(btn, async () => {
            const action = btn.dataset.action;
            try {
                await postJSON("/api/service", { action });
                setActionResult("sing-box " + action + " — ok", true);
                await fetchState();
            } catch (err) {
                setActionResult("sing-box " + action + " failed: " + err.message, false);
            }
        }));
    }

    // ── profiles ────────────────────────────────────────────────────────

    function escapeHTML(str) {
        return String(str).replace(/[&<>"']/g, (c) => ({
            "&": "&amp;", "<": "&lt;", ">": "&gt;",
            '"': "&quot;", "'": "&#39;",
        }[c]));
    }

    async function fetchProfiles() {
        const list = $("#profile-list");
        // Don't clobber an open editor: auto-refresh would otherwise
        // wipe the form mid-typing once the 30s tick fires.
        if (list.querySelector(".profile-editor")) {
            return;
        }
        try {
            const r = await fetch("/api/profiles", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const data = await r.json();
            const profiles = data.profiles || [];

            if (profiles.length === 0) {
                list.innerHTML = `<li class="profile-empty muted">No profiles yet — add one with a vless:// URL below.</li>`;
                return;
            }

            list.innerHTML = profiles.map((p) => {
                const cached = latencyCache[p.id];
                const pingPill = cached ? renderPingPill(cached) : "";
                const transport = (p.type || "tcp") + "+" + (p.security || "reality");
                return `
                <li class="profile-row${p.active ? " is-active" : ""}" data-id="${escapeHTML(p.id)}">
                    <div class="profile-info">
                        <div class="profile-name">${escapeHTML(p.name)}${p.active ? ` <span class="pill pill-ok">ACTIVE</span>` : ""} <span class="pill pill-muted">${escapeHTML(transport)}</span> ${pingPill}</div>
                        <div class="profile-meta">${escapeHTML(p.server)}:${p.port} · uuid ${escapeHTML(p.uuid_mask)}${p.flow ? " · flow " + escapeHTML(p.flow) : ""}${p.path ? " · path " + escapeHTML(p.path) : ""}</div>
                    </div>
                    <div class="profile-actions">
                        ${p.active
                            ? (lastRunning
                                ? `<button class="btn-action btn-danger"  data-act="stop"     data-id="${escapeHTML(p.id)}">Stop</button>`
                                : `<button class="btn-action btn-primary" data-act="activate" data-id="${escapeHTML(p.id)}">Activate</button>`)
                            : `<button class="btn-action btn-primary" data-act="activate" data-id="${escapeHTML(p.id)}">Activate</button>`}
                        <button class="btn-action" data-act="test" data-id="${escapeHTML(p.id)}">Test</button>
                        <button class="btn-action" data-act="edit" data-id="${escapeHTML(p.id)}">Edit</button>
                        ${p.active ? "" : `<button class="btn-action btn-danger"  data-act="delete"   data-id="${escapeHTML(p.id)}">Delete</button>`}
                    </div>
                </li>
            `}).join("");
        } catch (err) {
            list.innerHTML = `<li class="profile-empty muted">Failed to load profiles: ${escapeHTML(err.message)}</li>`;
        }
    }

    async function importVless() {
        const url = $("#vless-url").value.trim();
        const name = $("#vless-name").value.trim();
        if (!url) {
            setActionResult("VLESS URL is empty", false);
            return;
        }
        const btn = $("#vless-import");
        await setBusy(btn, async () => {
            try {
                const body = name ? { url, name } : { url };
                const data = await postJSON("/api/profiles/import-vless", body);
                setActionResult(`Profile "${data.name}" imported`, true);
                $("#vless-url").value = "";
                $("#vless-name").value = "";
                await fetchProfiles();
                await fetchState();
            } catch (err) {
                setActionResult("Import failed: " + err.message, false);
            }
        });
    }

    // ── latency test ───────────────────────────────────────────────────
    //
    // We cache the last result per profile so reopening the panel
    // doesn't blank out previous timings. Cleared on full page reload.
    const latencyCache = {};   // id → { ok, delayMs, error, at }

    // Last sing-box running state seen via fetchState. Drives the
    // active-profile row's button: when running we show "Stop"; when
    // stopped we show "Activate" so the user can resume the same
    // profile without first hunting for a Start button elsewhere.
    let lastRunning = null;

    function renderPingPill(r) {
        if (r.ok) {
            const cls = r.delayMs < 200 ? "pill-ok" :
                        r.delayMs < 500 ? "pill-warn" : "pill-bad";
            return `<span class="pill ${cls}">${r.delayMs} ms</span>`;
        }
        // Pick a short label that matches the actual failure mode so
        // the pill itself is informative, not just a tooltip.
        const err = (r.error || "").toLowerCase();
        let label = "error";
        if (err.includes("not running"))      label = "stopped";
        else if (err.includes("timeout"))     label = "timeout";
        else if (err.includes("refused"))     label = "refused";
        else if (err.includes("no such"))     label = "missing";
        return `<span class="pill pill-bad" title="${escapeHTML(r.error || "")}">${label}</span>`;
    }

    async function testProfile(id) {
        try {
            const r = await fetch("/api/profiles/" + encodeURIComponent(id) + "/delay", {
                credentials: "same-origin",
            });
            if (!r.ok) {
                const j = await r.json().catch(() => ({}));
                throw new Error(j.error || ("HTTP " + r.status));
            }
            const data = await r.json();
            latencyCache[id] = data.ok
                ? { ok: true,  delayMs: data.delay_ms, at: Date.now() }
                : { ok: false, error: data.error || "failed", at: Date.now() };
        } catch (err) {
            latencyCache[id] = { ok: false, error: err.message, at: Date.now() };
        }
        await fetchProfiles();
    }

    async function testAllProfiles() {
        const btn = $("#test-all");
        await setBusy(btn, async () => {
            try {
                const r = await fetch("/api/profiles", { credentials: "same-origin" });
                if (!r.ok) throw new Error("HTTP " + r.status);
                const data = await r.json();
                const profiles = data.profiles || [];
                if (profiles.length === 0) {
                    setActionResult("No profiles to test", false);
                    return;
                }
                setActionResult(`Testing ${profiles.length} profile(s)…`, true);
                // Sequential: clash-API does the proxy probe; serial
                // avoids overlapping probes through the same upstream.
                for (const p of profiles) {
                    await testProfile(p.id);
                }
                setActionResult("Latency test complete", true);
            } catch (err) {
                setActionResult("Test all failed: " + err.message, false);
            }
        });
    }

    async function profileAction(btn) {
        const id = btn.dataset.id;
        const act = btn.dataset.act;
        if (!id || !act) return;
        if (act === "edit") {
            await openProfileEditor(id, btn);
            return;
        }
        if (act === "test") {
            await setBusy(btn, async () => {
                await testProfile(id);
            });
            return;
        }
        await setBusy(btn, async () => {
            try {
                if (act === "stop") {
                    // Mirrors the Stop button in the Sing-box card —
                    // tearing the tunnel down via the active-profile
                    // row is the natural inverse of Activate.
                    await postJSON("/api/service", { action: "stop" });
                    setActionResult("sing-box stopped", true);
                } else if (act === "activate") {
                    const data = await postJSON("/api/profiles/" + encodeURIComponent(id) + "/activate", {});
                    const note =
                        data.switched       ? " (instant switch)" :
                        data.reloaded       ? " (reloaded)"        :
                        data.started        ? " (started)"         :
                        data.pending_switch ? " (will start when the side switch flips ON)" :
                                              " (will start on next Start)";
                    setActionResult(`Profile "${data.profile_name}" activated${note}`,
                                    !data.pending_switch);
                    // The latency pill from any prior test is now stale —
                    // it was measured against a different selector default.
                    // Drop it and re-test in the background so the user
                    // sees a fresh result without staring at the old badge.
                    delete latencyCache[id];
                    testProfile(id).catch(() => {});
                } else if (act === "delete") {
                    if (!confirm("Delete this profile?")) return;
                    await fetch("/api/profiles/" + encodeURIComponent(id), {
                        method: "DELETE",
                        credentials: "same-origin",
                    }).then(async (r) => {
                        if (!r.ok) {
                            const j = await r.json().catch(() => ({}));
                            throw new Error(j.error || ("HTTP " + r.status));
                        }
                    });
                    setActionResult("Profile deleted", true);
                }
                await fetchProfiles();
                await fetchState();
            } catch (err) {
                setActionResult(act + " failed: " + err.message, false);
            }
        });
    }

    async function openProfileEditor(id, anchorBtn) {
        const row = anchorBtn.closest(".profile-row");
        if (!row) return;

        // Close any other open editors first.
        $$(".profile-editor").forEach((el) => el.remove());
        if (row.classList.contains("editing")) {
            row.classList.remove("editing");
            return;
        }
        row.classList.add("editing");

        // Pull the full profile (un-masked UUID) to pre-fill the form.
        let profile;
        try {
            const r = await fetch("/api/profiles?reveal=1", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const data = await r.json();
            profile = (data.profiles || []).find((p) => p.id === id);
            if (!profile) throw new Error("not found");
        } catch (err) {
            setActionResult("Failed to load profile: " + err.message, false);
            row.classList.remove("editing");
            return;
        }

        const editor = document.createElement("div");
        editor.className = "profile-editor";
        const sel = (val, opts) => opts.map((o) => `<option value="${o}"${o === val ? " selected" : ""}>${o}</option>`).join("");
        editor.innerHTML = `
            <div class="profile-form">
                <div class="form-grid">
                    <label class="lbl-block">Name <input type="text" data-f="name" value="${escapeHTML(profile.name || "")}"></label>
                    <label class="lbl-block">Flow <input type="text" data-f="flow" value="${escapeHTML(profile.flow || "")}"></label>
                    <label class="lbl-block">Server <input type="text" data-f="server" value="${escapeHTML(profile.server || "")}"></label>
                    <label class="lbl-block">Port <input type="number" data-f="port" min="1" max="65535" value="${profile.port || ""}"></label>
                    <label class="lbl-block lbl-wide">UUID <input type="text" data-f="uuid" value="${escapeHTML(profile.uuid || "")}"></label>
                    <label class="lbl-block">Transport <select data-f="type">${sel(profile.type || "tcp", ["tcp","ws"])}</select></label>
                    <label class="lbl-block">Security <select data-f="security">${sel(profile.security || "reality", ["reality","tls"])}</select></label>
                    <label class="lbl-block">SNI <input type="text" data-f="sni" value="${escapeHTML(profile.sni || "")}"></label>
                    <label class="lbl-block">Fingerprint <input type="text" data-f="fingerprint" value="${escapeHTML(profile.fingerprint || "")}"></label>
                    <label class="lbl-block lbl-wide">Public key (Reality) <input type="text" data-f="public_key" value="${escapeHTML(profile.public_key || "")}"></label>
                    <label class="lbl-block">Short ID (Reality) <input type="text" data-f="short_id" value="${escapeHTML(profile.short_id || "")}"></label>
                    <label class="lbl-block">WS Path <input type="text" data-f="path" placeholder="/" value="${escapeHTML(profile.path || "")}"></label>
                    <label class="lbl-block">WS Host <input type="text" data-f="host" placeholder="(empty = same as Server)" value="${escapeHTML(profile.host || "")}"></label>
                </div>
                <div class="action-row">
                    <button class="btn-action btn-primary" data-edit-act="save">Save</button>
                    <button class="btn-action" data-edit-act="cancel">Cancel</button>
                </div>
            </div>
        `;
        row.after(editor);

        editor.querySelector('[data-edit-act="cancel"]').addEventListener("click", () => {
            editor.remove();
            row.classList.remove("editing");
        });
        editor.querySelector('[data-edit-act="save"]').addEventListener("click", async (e) => {
            const btn = e.target;
            await setBusy(btn, async () => {
                const body = {};
                editor.querySelectorAll("[data-f]").forEach((el) => {
                    const f = el.dataset.f;
                    let v = el.value;
                    if (f === "port") v = Number(v);
                    body[f] = v;
                });
                try {
                    const data = await fetch("/api/profiles/" + encodeURIComponent(id), {
                        method: "PATCH",
                        headers: { "Content-Type": "application/json" },
                        credentials: "same-origin",
                        body: JSON.stringify(body),
                    }).then(async (r) => {
                        const j = await r.json().catch(() => ({}));
                        if (!r.ok) throw new Error(j.error || ("HTTP " + r.status));
                        return j;
                    });
                    setActionResult(`Profile saved${data.reloaded ? " (sing-box reloaded)" : ""}`, true);
                    editor.remove();
                    row.classList.remove("editing");
                    await fetchProfiles();
                    await fetchState();
                } catch (err) {
                    setActionResult("Save failed: " + err.message, false);
                }
            });
        });
    }

    // ── live + logs ─────────────────────────────────────────────────────

    function fmtBytes(n) {
        if (!n || n < 0) return "0 B";
        const units = ["B", "KB", "MB", "GB", "TB"];
        let i = 0;
        while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
        return n.toFixed(n >= 100 || i === 0 ? 0 : (n >= 10 ? 1 : 2)) + " " + units[i];
    }

    function fmtRate(bytesPerSec) {
        if (!bytesPerSec) return "0 B/s";
        return fmtBytes(bytesPerSec) + "/s";
    }

    async function fetchLive() {
        try {
            const r = await fetch("/api/live", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const s = await r.json();

            // Exit IP — surfaced in two places: the Live card header
            // (Monitor tab) and the Profiles card header (Control tab).
            // Both reflect the same poller value; the duplicate keeps
            // the relevant pill visible whichever tab is active.
            renderBlock(cell("live_exit_ip"), s.exit_ip, (v) => {
                if (!v || !v.ip) return { cls: "pill-muted", text: "—" };
                return { cls: "pill-ok", text: v.ip };
            });
            renderBlock(cell("profiles_exit_ip"), s.exit_ip, (v) => {
                if (!v || !v.ip) return { cls: "pill-muted", text: "—" };
                return { cls: "pill-ok", text: v.ip };
            });

            // Traffic
            const t = s.traffic && s.traffic.value;
            const ok = s.traffic && s.traffic.ok;
            const upRate   = ok ? fmtRate(t.up_rate)   : "—";
            const downRate = ok ? fmtRate(t.down_rate) : "—";
            const conns    = ok ? String(t.connections) : "—";
            setPill(cell("live_up_rate"),   ok ? (t.up_rate   > 0 ? "pill-ok" : "pill-muted") : "pill-warn", upRate);
            setPill(cell("live_down_rate"), ok ? (t.down_rate > 0 ? "pill-ok" : "pill-muted") : "pill-warn", downRate);
            setPill(cell("live_conn_count"),ok ? "pill-ok" : "pill-muted", conns);

            const totals = $("#live-totals");
            if (ok) {
                totals.textContent = "↑ " + fmtBytes(t.up_total) + "  ↓ " + fmtBytes(t.down_total) + "  · total since sing-box start";
            } else {
                totals.textContent = (s.traffic && s.traffic.error) ? ("error: " + s.traffic.error) : "—";
            }

            // Top flows
            const tbody = $("#flow-table tbody");
            const flows = (s.top_flows && s.top_flows.value) || [];
            if (flows.length === 0) {
                tbody.innerHTML = `<tr><td colspan="4" class="muted">No active connections.</td></tr>`;
            } else {
                tbody.innerHTML = flows.map((f) => `
                    <tr>
                        <td>
                            <div class="flow-host">${escapeHTML(f.host || f.destination)}</div>
                            ${f.host ? `<div class="flow-dest">${escapeHTML(f.destination)}</div>` : ""}
                        </td>
                        <td>${escapeHTML(f.network || "")}</td>
                        <td class="num">${fmtBytes(f.up)}</td>
                        <td class="num">${fmtBytes(f.down)}</td>
                    </tr>
                `).join("");
            }
        } catch (err) {
            $("#live-totals").textContent = "Failed to load /api/live: " + err.message;
        }
    }

    // ── log streaming via Server-Sent Events ───────────────────────────
    //
    // We replaced the 3-second polling with an EventSource that pushes
    // each new line as soon as sing-box writes it. Auto-reconnect is
    // built into the browser API; we just decide whether to render
    // lines as they arrive.

    const LOG_BUFFER_LINES = 1000;   // hard cap so DOM doesn't grow forever
    let logEventSource = null;
    let logBuffer = [];

    function logViewIsAtBottom() {
        const view = $("#log-view");
        return (view.scrollHeight - view.scrollTop - view.clientHeight) < 8;
    }

    function renderLogBuffer(forceScroll) {
        const view = $("#log-view");
        view.textContent = logBuffer.length ? logBuffer.join("\n") : "(empty)";
        const autoScroll = $("#log-autoscroll").checked;
        if (autoScroll && (forceScroll || logViewIsAtBottom())) {
            view.scrollTop = view.scrollHeight;
        }
    }

    function appendLogLine(line) {
        const wasAtBottom = logViewIsAtBottom();
        logBuffer.push(line);
        if (logBuffer.length > LOG_BUFFER_LINES) {
            logBuffer = logBuffer.slice(logBuffer.length - LOG_BUFFER_LINES);
        }
        renderLogBuffer(wasAtBottom);
    }

    function startLogStream() {
        stopLogStream();
        logBuffer = [];
        $("#log-view").textContent = "Connecting…";

        // EventSource carries the browser's cached basic-auth header
        // automatically for same-origin requests.
        const es = new EventSource("/api/logs/stream?backfill=200");

        es.onmessage = (e) => {
            appendLogLine(e.data);
        };
        es.onerror = () => {
            // Browser will auto-retry. Keep what's already shown so
            // the user sees the stream "freeze" instead of going blank.
            // If we never connected at all, show a small notice.
            if (logBuffer.length === 0) {
                $("#log-view").textContent = "Log stream disconnected — retrying…";
            }
        };
        logEventSource = es;
    }

    function stopLogStream() {
        if (logEventSource) {
            logEventSource.close();
            logEventSource = null;
        }
    }

    document.addEventListener("DOMContentLoaded", () => {
        bindToggle("killswitch",  "Killswitch");
        bindToggle("bind_switch", "Bind switch");
        $$("button.btn-action").forEach((b) => {
            if (b.dataset.action) bindAction(b);
        });
        $("#refresh").addEventListener("click", () => {
            fetchState();
            fetchProfiles();
            fetchLive();
            // Logs are live-streamed, but a manual refresh re-opens
            // the stream which clears any disconnect notice.
            startLogStream();
        });
        $("#vless-import").addEventListener("click", importVless);
        $("#test-all").addEventListener("click", testAllProfiles);

        // Profile row buttons are added dynamically — delegate.
        $("#profile-list").addEventListener("click", (e) => {
            const btn = e.target.closest("button[data-act]");
            if (btn) profileAction(btn);
        });

        // Tab switcher — Control / Monitor. Hidden panes still get
        // their data refreshed in the background so switching tabs
        // is instant; cheap polls won't pile up on a slow router.
        document.querySelectorAll(".tabs button[data-tab]").forEach((tab) => {
            tab.addEventListener("click", () => {
                document.querySelectorAll(".tabs button[data-tab]").forEach((t) => {
                    t.setAttribute("aria-selected", t === tab ? "true" : "false");
                });
                document.querySelectorAll("section[data-pane]").forEach((s) => {
                    s.hidden = (s.getAttribute("data-pane") !== tab.getAttribute("data-tab"));
                });
            });
        });

        fetchState();
        fetchProfiles();
        fetchLive();
        startLogStream();

        // Poll cadence picks: state changes via user actions are
        // applied via fetchState() right after the action call, so
        // background polling can be lazy. Live traffic numbers tick
        // ~every 5s, that's plenty for "is the tunnel busy?".
        setInterval(fetchState,    10000);
        setInterval(fetchProfiles, 30000);
        setInterval(fetchLive,     5000);

        // Log stream toggle — checkbox now means "live stream on/off".
        $("#log-autorefresh").addEventListener("change", (e) => {
            if (e.target.checked) startLogStream();
            else stopLogStream();
        });

        // Pause stream when tab is hidden to avoid accumulating data.
        document.addEventListener("visibilitychange", () => {
            if (document.hidden) {
                stopLogStream();
            } else if ($("#log-autorefresh").checked) {
                startLogStream();
            }
        });
    });
})();
