// xray-panel-cli — Phase 2B UI:
//   - polls /api/state every 5s and renders the status table
//   - toggles for killswitch and bind_switch (POST /api/{killswitch,bind_switch})
//   - start/stop/restart/reload buttons (POST /api/service)
//
// No build step, no framework. Will be reworked in 2D / 2F.

(function () {
    const $ = (sel) => document.querySelector(sel);
    const $$ = (sel) => Array.from(document.querySelectorAll(sel));

    const stateRow = (key) => $(`#state-table td[data-key="${key}"]`);
    const toggleBtn = (name) => $(`button[data-toggle="${name}"]`);
    const actionResult = $("#action-result");

    function pill(cls, text) {
        return `<span class="pill ${cls}">${text}</span>`;
    }

    function renderBlock(td, block, mapper) {
        if (!block) {
            td.innerHTML = pill("pill-muted", "—");
            return;
        }
        if (block.error) {
            td.innerHTML = pill("pill-warn", "ошибка: " + block.error);
            return;
        }
        const m = mapper(block.value);
        td.innerHTML = pill(m.cls, m.text);
    }

    function setActionResult(msg, ok) {
        actionResult.textContent = msg;
        actionResult.className = "muted " + (ok ? "action-ok" : "action-bad");
    }

    function setToggleVisual(name, on) {
        const btn = toggleBtn(name);
        if (!btn) return;
        btn.dataset.state = on ? "on" : "off";
        btn.textContent = on ? "ON" : "OFF";
    }

    async function fetchState() {
        try {
            const r = await fetch("/api/state", { credentials: "same-origin" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            const s = await r.json();

            renderBlock(stateRow("service"), s.service, (v) =>
                v ? { cls: "pill-ok", text: "running" } : { cls: "pill-bad", text: "stopped" });
            renderBlock(stateRow("tun"), s.tun, (v) =>
                v ? { cls: "pill-ok", text: "up" } : { cls: "pill-bad", text: "down" });
            renderBlock(stateRow("physical_switch"), s.physical_switch, (v) =>
                v === "on"  ? { cls: "pill-ok",    text: "ON" } :
                v === "off" ? { cls: "pill-muted", text: "OFF" } :
                              { cls: "pill-warn",  text: v || "unknown" });
            renderBlock(stateRow("killswitch"), s.killswitch, (v) =>
                v ? { cls: "pill-ok", text: "ON" } : { cls: "pill-muted", text: "OFF" });
            renderBlock(stateRow("bind_switch"), s.bind_switch, (v) =>
                v ? { cls: "pill-ok", text: "ON" } : { cls: "pill-muted", text: "OFF" });
            renderBlock(stateRow("enabled"), s.enabled, (v) =>
                v ? { cls: "pill-ok", text: "enabled" } : { cls: "pill-muted", text: "disabled" });

            $("#generated-at").textContent = "Обновлено: " + s.generated_at;

            if (s.killswitch && s.killswitch.ok) setToggleVisual("killswitch", !!s.killswitch.value);
            if (s.bind_switch && s.bind_switch.ok) setToggleVisual("bind_switch", !!s.bind_switch.value);
        } catch (err) {
            setActionResult("Не удалось получить /api/state: " + err.message, false);
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
        try {
            await fn();
        } finally {
            btn.disabled = before;
        }
    }

    function bindToggle(name) {
        const btn = toggleBtn(name);
        if (!btn) return;
        btn.addEventListener("click", () => setBusy(btn, async () => {
            const desired = btn.dataset.state !== "on";
            try {
                await postJSON("/api/" + name, { on: desired });
                setActionResult(name + " → " + (desired ? "ON" : "OFF"), true);
                await fetchState();
            } catch (err) {
                setActionResult(name + " failed: " + err.message, false);
            }
        }));
    }

    function bindAction(btn) {
        btn.addEventListener("click", () => setBusy(btn, async () => {
            const action = btn.dataset.action;
            try {
                await postJSON("/api/service", { action });
                setActionResult("service " + action + " → ok", true);
                await fetchState();
            } catch (err) {
                setActionResult("service " + action + " failed: " + err.message, false);
            }
        }));
    }

    document.addEventListener("DOMContentLoaded", () => {
        bindToggle("killswitch");
        bindToggle("bind_switch");
        $$("button.btn-action").forEach(bindAction);
        $("#refresh").addEventListener("click", fetchState);
        fetchState();
        setInterval(fetchState, 5000);
    });
})();
