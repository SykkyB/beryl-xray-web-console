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

    document.addEventListener("DOMContentLoaded", () => {
        bindToggle("killswitch",  "Killswitch");
        bindToggle("bind_switch", "Bind switch");
        $$("button.btn-action").forEach((b) => {
            if (b.dataset.action) bindAction(b);
        });
        $("#refresh").addEventListener("click", () => { fetchState(); fetchProfiles(); });
        $("#vless-import").addEventListener("click", importVless);

        // Profile row buttons are added dynamically — delegate.
        $("#profile-list").addEventListener("click", (e) => {
            const btn = e.target.closest("button[data-act]");
            if (btn) profileAction(btn);
        });

        fetchState();
        fetchProfiles();
        setInterval(fetchState, 5000);
        setInterval(fetchProfiles, 15000);
    });
})();
