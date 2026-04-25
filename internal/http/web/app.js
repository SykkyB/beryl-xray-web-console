// xray-panel-cli — Phase 2A placeholder script.
// Just hits /api/ping to confirm backend reachability.

(function () {
    const out = document.getElementById("ping-result");

    fetch("/api/ping", { credentials: "same-origin" })
        .then((r) => {
            if (!r.ok) throw new Error("HTTP " + r.status);
            return r.json();
        })
        .then((data) => {
            out.textContent = "Backend OK: " + JSON.stringify(data);
        })
        .catch((err) => {
            out.textContent = "Backend FAIL: " + err.message;
        });
})();
