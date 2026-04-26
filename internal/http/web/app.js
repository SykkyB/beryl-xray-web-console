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
            renderBlock(cell("tun"), s.tun, (v) =>
                v ? { cls: "pill-ok", text: "up" } : { cls: "pill-bad", text: "down" });
            renderBlock(cell("enabled"), s.enabled, (v) =>
                v ? { cls: "pill-ok", text: "enabled" } : { cls: "pill-muted", text: "disabled" });

            renderBlock(cell("active_profile"), s.active_profile, (v) =>
                v && v.name ? { cls: "pill-ok", text: v.name } : { cls: "pill-muted", text: "none" });

            renderBlock(cell("killswitch"), s.killswitch, (v) =>
                v ? { cls: "pill-ok", text: "ON" } : { cls: "pill-muted", text: "OFF" });

            renderBlock(cell("physical_switch"), s.physical_switch, (v) =>
                v === "on"  ? { cls: "pill-ok",    text: "ON" } :
                v === "off" ? { cls: "pill-muted", text: "OFF" } :
                              { cls: "pill-warn",  text: v || "unknown" });
            renderBlock(cell("bind_switch"), s.bind_switch, (v) =>
                v ? { cls: "pill-ok", text: "ON" } : { cls: "pill-muted", text: "OFF" });

            $("#generated-at").textContent = "Updated: " + formatTime(s.generated_at);

            if (s.killswitch && s.killswitch.ok) setToggleVisual("killswitch", !!s.killswitch.value);
            if (s.bind_switch && s.bind_switch.ok) setToggleVisual("bind_switch", !!s.bind_switch.value);
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
        try {
            const r = await fetch("/api/profiles", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const data = await r.json();
            const profiles = data.profiles || [];

            if (profiles.length === 0) {
                list.innerHTML = `<li class="profile-empty muted">No profiles yet — add one with a vless:// URL below.</li>`;
                return;
            }

            list.innerHTML = profiles.map((p) => `
                <li class="profile-row${p.active ? " is-active" : ""}">
                    <div class="profile-info">
                        <div class="profile-name">${escapeHTML(p.name)}${p.active ? ` <span class="pill pill-ok">ACTIVE</span>` : ""}</div>
                        <div class="profile-meta">${escapeHTML(p.server)}:${p.port} · uuid ${escapeHTML(p.uuid_mask)}${p.flow ? " · flow " + escapeHTML(p.flow) : ""}</div>
                    </div>
                    <div class="profile-actions">
                        ${p.active ? "" : `<button class="btn-action btn-primary" data-act="activate" data-id="${escapeHTML(p.id)}">Activate</button>`}
                        ${p.active ? "" : `<button class="btn-action btn-danger"  data-act="delete"   data-id="${escapeHTML(p.id)}">Delete</button>`}
                    </div>
                </li>
            `).join("");
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

    async function profileAction(btn) {
        const id = btn.dataset.id;
        const act = btn.dataset.act;
        if (!id || !act) return;
        await setBusy(btn, async () => {
            try {
                if (act === "activate") {
                    const data = await postJSON("/api/profiles/" + encodeURIComponent(id) + "/activate", {});
                    const note = data.reloaded ? " (reloaded)" : " (will start on next Start)";
                    setActionResult(`Profile "${data.profile_name}" activated${note}`, true);
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

            // Exit IP
            renderBlock(cell("live_exit_ip"), s.exit_ip, (v) => {
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

    async function fetchLogs() {
        const view = $("#log-view");
        const wasAtBottom = (view.scrollHeight - view.scrollTop - view.clientHeight) < 8;
        try {
            const r = await fetch("/api/logs?lines=200", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const data = await r.json();
            view.textContent = (data.lines || []).join("\n") || "(empty)";
            const autoScroll = $("#log-autoscroll").checked;
            if (autoScroll && wasAtBottom) {
                view.scrollTop = view.scrollHeight;
            }
        } catch (err) {
            view.textContent = "Failed to load logs: " + err.message;
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
            fetchLogs();
        });
        $("#vless-import").addEventListener("click", importVless);

        // Profile row buttons are added dynamically — delegate.
        $("#profile-list").addEventListener("click", (e) => {
            const btn = e.target.closest("button[data-act]");
            if (btn) profileAction(btn);
        });

        fetchState();
        fetchProfiles();
        fetchLive();
        fetchLogs();

        setInterval(fetchState,    5000);
        setInterval(fetchProfiles, 15000);
        setInterval(fetchLive,     2000);

        // Logs auto-refresh: every 3s when checkbox is ticked.
        let logTimer = setInterval(fetchLogs, 3000);
        $("#log-autorefresh").addEventListener("change", (e) => {
            clearInterval(logTimer);
            if (e.target.checked) {
                logTimer = setInterval(fetchLogs, 3000);
                fetchLogs();
            }
        });
    });
})();
