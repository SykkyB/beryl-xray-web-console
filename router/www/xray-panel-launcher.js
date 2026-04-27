// xray-panel-launcher.js — injects a launch link into the GL.iNet stock
// admin UI for the xray-panel-cli at :9092.
//
// One entry: appended INSIDE the expanded VPN sidebar submenu as the
// last item (after Tor), styled like a native sub-row. Cloned from
// an existing submenu item so indent / icon-slot / font / padding all
// match exactly. Click opens the panel in a new tab.
//
// No fallback / overlay: if the anchor can't be found we retry until
// it appears (the GL.iNet UI is a Vue SPA — anchors land milliseconds
// after DOMContentLoaded). If retries time out, the user falls back
// to opening :9092 directly. That's strictly better than overlay
// garbage.
//
// To remove the integration entirely:
//   ssh beryl 'cp /www/gl_home.html.bak /www/gl_home.html; rm -f /www/xray-panel-launcher.js'
(function () {
    "use strict";

    var PORT = 9092;
    var SIDEBAR_ID = "xray-panel-sidebar";
    var MAX_TRIES  = 60;       // 60 × 250ms = 15s, plenty for SPA boot
    var POLL_MS    = 250;

    function panelURL() {
        return location.protocol.replace("https:", "http:") +
               "//" + location.hostname + ":" + PORT + "/";
    }

    function newAnchor(label) {
        var a = document.createElement("a");
        a.href   = panelURL();
        a.target = "_blank";
        a.rel    = "noopener";
        a.title  = "XRAY sing-box client panel (" + a.href + ")";
        a.textContent = label;
        return a;
    }

    // Poll until check() returns truthy; then call onFound(value). Uses
    // setTimeout with a small interval so the page can paint between
    // attempts. Caps at MAX_TRIES to avoid runaway loops.
    function whenReady(check, onFound) {
        var tries = 0;
        function attempt() {
            try {
                var v = check();
                if (v) { onFound(v); return; }
            } catch (e) {}
            if (++tries < MAX_TRIES) setTimeout(attempt, POLL_MS);
        }
        attempt();
    }

    // ── sidebar — last item inside the VPN expanded submenu ─────────
    //
    // Strategy: find the top-level VPN menu row, walk up to its <li>,
    // locate the nested <ul> submenu (containing VPN Dashboard /
    // OpenVPN Client / WireGuard Server / Tor / etc.), clone its LAST
    // child <li> (e.g. "Tor") so we inherit the sub-item's exact
    // indent / icon-slot / typography, replace the label, strip
    // router wiring, wrap in an anchor → append at the end of the
    // submenu.
    //
    // When the user collapses the VPN section, our entry collapses
    // with it — the natural behaviour, since we live inside that <ul>.
    function findVPNRowLeaf() {
        var els = document.querySelectorAll("span, a, div, em, b");
        for (var i = 0; i < els.length; i++) {
            var el = els[i];
            if (el.children.length > 0) continue;
            var t = (el.textContent || "").trim();
            if (/^vpn$/i.test(t)) return el;
        }
        return null;
    }

    function ancestorLI(node) {
        for (var d = 0; d < 8 && node; d++) {
            if (node.tagName === "LI") return node;
            node = node.parentElement;
        }
        return null;
    }

    function findVPNSubmenuTemplate() {
        // GL.iNet's sidebar nests submenus — VPN <li> often contains
        // an outer <ul> whose only direct child is a wrapper <li>
        // containing the real submenu <ul>. Picking the outer one and
        // cloning its first <li> would clone ALL VPN items.
        //
        // Robust approach: find a leaf labelled with a known VPN
        // submenu entry (Tor / WireGuard Server / OpenVPN Server) —
        // that text only appears once on the page in the right place.
        // Walk up to its containing <li>; that's our template.
        var rxKnown = /^(tor|wireguard server|wireguard client|openvpn server|openvpn client|vpn dashboard)$/i;
        var els = document.querySelectorAll("span, a, div, em, b");
        for (var i = 0; i < els.length; i++) {
            var el = els[i];
            if (el.children.length > 0) continue;
            var t = (el.textContent || "").trim();
            if (rxKnown.test(t)) {
                var li = ancestorLI(el);
                // It must live inside a <ul> with at least 2 sibling
                // submenu rows (otherwise we'd append to the wrong
                // place — e.g. a non-VPN solo menu).
                if (li && li.parentNode && li.parentNode.children.length >= 2) {
                    return li;
                }
            }
        }
        return null;
    }

    function renderSidebar() {
        if (document.getElementById(SIDEBAR_ID)) return;

        whenReady(findVPNSubmenuTemplate, function (template) {
            if (document.getElementById(SIDEBAR_ID)) return;
            if (!template || !template.parentNode) return;
            var sub = template.parentNode;
            var clone = template.cloneNode(true);
            clone.id = SIDEBAR_ID;

            // Strip any router-link wiring — Vue attaches navigation
            // via @click which doesn't survive cloneNode, but href on
            // child <a> tags does.
            var hrefEls = clone.querySelectorAll("[href]");
            for (var h = 0; h < hrefEls.length; h++) {
                hrefEls[h].removeAttribute("href");
                hrefEls[h].removeAttribute("target");
            }
            var clickEls = clone.querySelectorAll("[onclick]");
            for (var c = 0; c < clickEls.length; c++) {
                clickEls[c].removeAttribute("onclick");
            }
            // Drop any "active" / "router-link-active" classes the
            // template happened to carry — our entry is never the
            // currently-routed page (it opens in a new tab).
            var activeEls = clone.querySelectorAll("[class*='active']");
            for (var a = 0; a < activeEls.length; a++) {
                activeEls[a].className = activeEls[a].className
                    .replace(/\b\S*active\S*\b/g, "")
                    .replace(/\s+/g, " ").trim();
            }
            if (typeof clone.className === "string") {
                clone.className = clone.className
                    .replace(/\b\S*active\S*\b/g, "")
                    .replace(/\s+/g, " ").trim();
            }

            // Replace the label leaf (whatever text the template had)
            // with "VPN XRAY". Pick the deepest leaf with non-empty
            // text — that's the visible label.
            var leaves = clone.querySelectorAll("*");
            var labelLeaf = null;
            for (var i = 0; i < leaves.length; i++) {
                var l = leaves[i];
                if (l.children.length > 0) continue;
                var t = (l.textContent || "").trim();
                if (t.length > 0 && !/^[ -　\s]+$/.test(t)) {
                    labelLeaf = l;
                }
            }
            if (labelLeaf) {
                labelLeaf.textContent = "VPN XRAY";
            } else {
                clone.textContent = "VPN XRAY";
            }

            // Wrap the clone's contents in an anchor that opens our
            // panel in a new tab, bypassing the SPA router.
            var url = panelURL();
            var wrapper = document.createElement("a");
            wrapper.href = url;
            wrapper.target = "_blank";
            wrapper.rel = "noopener";
            wrapper.title = "XRAY sing-box client panel (" + url + ")";
            wrapper.style.cssText = "display:block;color:inherit;text-decoration:none;cursor:pointer";
            while (clone.firstChild) wrapper.appendChild(clone.firstChild);
            clone.appendChild(wrapper);

            // Append at the end of the VPN submenu.
            sub.appendChild(clone);
        });
    }

    // ── home-page service icon — turn green when sing-tun is up ─────
    //
    // GL.iNet's home page (#/internet) renders a row of service icons:
    // AdGuard, IPv6, VPN, Tor. Native firmware lights "VPN" up in the
    // brand teal when one of their stock VPN clients (WG / OVPN) is
    // connected; ours is invisible to that logic, so the icon stays
    // grey even when our sing-box tunnel is carrying traffic.
    //
    // Approach: poll a public probe endpoint on the panel. The probe
    // is an <img> load — no CORS preflight, no credentials, just
    // onload (200, tunnel up) vs onerror (404, tunnel down). The
    // launcher applies our CSS class to the icon container, which
    // styles the glyph + label in the GL.iNet teal regardless of what
    // the SPA's own state machine thinks.
    //
    // We don't try to drive their internal Vue store: that would be
    // brittle (minified field names) and would still fight back on
    // the next periodic refresh.
    var ICON_CSS_ID = "xray-vpn-icon-style";
    var ICON_ACTIVE_CLASS = "xray-vpn-active";
    var ICON_POLL_MS = 5000;
    // gl-blue-500 from GL.iNet's palette — distinct from the native
    // teal (#02b6d2) that lights up when their stock WG/OVPN clients
    // are connected, so it's obvious which indicator is talking.
    var ACTIVE_COLOR = "#5272f7";

    function injectIconCSS() {
        if (document.getElementById(ICON_CSS_ID)) return;
        var style = document.createElement("style");
        style.id = ICON_CSS_ID;
        // The topology icon may be a font glyph (color), an inline
        // SVG (fill / stroke), or a CSS-mask image (background-color
        // on the mask). Cover all four; harmless on the ones that
        // don't apply.
        var c = ACTIVE_COLOR;
        style.textContent =
            "." + ICON_ACTIVE_CLASS + ", " +
            "." + ICON_ACTIVE_CLASS + " * { " +
                "color: " + c + " !important; " +
                "fill: " + c + " !important; " +
                "stroke: " + c + " !important; " +
            "} " +
            "." + ICON_ACTIVE_CLASS + " svg path, " +
            "." + ICON_ACTIVE_CLASS + " svg circle, " +
            "." + ICON_ACTIVE_CLASS + " svg rect { " +
                "fill: " + c + " !important; " +
                "stroke: " + c + " !important; " +
            "}";
        document.head.appendChild(style);
    }

    // Find the home-page TOPOLOGY VPN service-cell. Confirmed DOM
    // structure (GL.iNet 4.8.1):
    //
    //   <div class="router-visual-wrapper">
    //     <div class="router-info">
    //       <ul class="app-list">
    //         <li> <span class="iconfont icon-adguard"></span> AdGuard </li>
    //         <li> <span class="iconfont icon-ipv6"></span> IPv6 </li>
    //         <li> <span class="iconfont icon-vpn1"></span> VPN </li>
    //         <li> <span class="iconfont icon-tor"></span> Tor </li>
    //
    // Pin to ul.app-list under .router-visual-wrapper / .router-info
    // to avoid picking up the sidebar VPN entry (which uses .icon-vpn
    // and lives in a different ul). Walk-up heuristics from earlier
    // versions hit a common ancestor and accidentally returned the
    // sidebar — this version uses no heuristics, just structure.
    function findVPNServiceCell() {
        var rows = document.querySelectorAll(
            ".router-visual-wrapper ul.app-list, " +
            ".router-info ul.app-list, " +
            "ul.app-list"
        );
        for (var r = 0; r < rows.length; r++) {
            var row = rows[r];
            // Sanity: the matched <ul> must look like the service
            // row — at least one sibling label confirms it. Cheap
            // guard against future SPA refactors that reuse the
            // class for unrelated lists.
            if (!/AdGuard|IPv6|Tor/.test(row.textContent || "")) continue;

            var items = row.querySelectorAll("li");
            for (var i = 0; i < items.length; i++) {
                if (/^vpn$/i.test((items[i].textContent || "").trim())) {
                    return items[i];
                }
            }
        }
        return null;
    }

    function panelOriginNoSlash() {
        return location.protocol.replace("https:", "http:") +
               "//" + location.hostname + ":" + PORT;
    }

    function probeUp(onResult) {
        var done = false;
        var img = new Image();
        var ts = Date.now();
        function finish(ok) {
            if (done) return;
            done = true;
            try { onResult(ok); } catch (e) {}
        }
        img.onload  = function () { finish(true); };
        img.onerror = function () { finish(false); };
        // Cache-bust so a 200 -> 404 transition is observed promptly
        // even on ISPs that aggressively cache image responses.
        img.src = panelOriginNoSlash() + "/api/up.png?ts=" + ts;
        // Hard bound: if neither fires in 4s assume "down".
        setTimeout(function () { finish(false); }, 4000);
    }

    // While our class is on the cell, intercept clicks at capture
    // phase and open the panel in a new tab instead of letting Vue
    // navigate to the stock VPN dashboard. Capture + stopImmediate
    // beats both capture- and bubble-phase listeners that the SPA
    // attached. When the class is removed, restore native behaviour.
    function bindCellClick(cell) {
        if (cell.__xrayClickBound) return;
        var handler = function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            try { window.open(panelURL(), "_blank", "noopener"); } catch (err) {}
        };
        cell.addEventListener("click", handler, true);
        cell.__xrayClickBound = handler;
        // Visible affordance — a hand cursor signals "this opens
        // something" even before the user clicks; the native cell
        // already navigates within the SPA, so we're not changing
        // the click-affordance, just the destination.
        cell.style.cursor = "pointer";
        cell.title = "Open XRAY sing-box panel";
    }

    function unbindCellClick(cell) {
        if (!cell.__xrayClickBound) return;
        cell.removeEventListener("click", cell.__xrayClickBound, true);
        delete cell.__xrayClickBound;
        cell.style.cursor = "";
        cell.title = "";
    }

    function applyVPNIconState(active) {
        var cell = findVPNServiceCell();

        // Stale-cleanup sweep: earlier launcher versions painted the
        // sidebar VPN entry by mistake. Strip the active class +
        // unbind any leftover click handler from anything that isn't
        // our chosen topology cell so users don't have to reload to
        // clear leftovers.
        var stale = document.querySelectorAll("." + ICON_ACTIVE_CLASS);
        for (var s = 0; s < stale.length; s++) {
            if (stale[s] !== cell) {
                stale[s].classList.remove(ICON_ACTIVE_CLASS);
                unbindCellClick(stale[s]);
            }
        }

        // Debug hook — `xrayLauncher` is queryable from DevTools.
        try {
            window.xrayLauncher = window.xrayLauncher || {};
            window.xrayLauncher.lastCell = cell;
            window.xrayLauncher.lastActive = active;
            window.xrayLauncher.lastTickAt = new Date().toISOString();
        } catch (e) {}

        if (!cell) {
            try { console.warn("[xray-panel] VPN topology icon NOT FOUND in DOM"); } catch (e) {}
            return;
        }
        var has = cell.classList.contains(ICON_ACTIVE_CLASS);
        if (active && !has) {
            cell.classList.add(ICON_ACTIVE_CLASS);
            bindCellClick(cell);
            try { console.log("[xray-panel] VPN icon → ACTIVE", cell); } catch (e) {}
        }
        if (!active && has) {
            cell.classList.remove(ICON_ACTIVE_CLASS);
            unbindCellClick(cell);
            try { console.log("[xray-panel] VPN icon → inactive", cell); } catch (e) {}
        }
    }

    function tickVPNIcon() {
        injectIconCSS();
        probeUp(applyVPNIconState);
    }

    function startVPNIconPoll() {
        // First tick fires immediately so the icon flips on page load
        // without waiting a full interval; subsequent ticks pace.
        tickVPNIcon();
        setInterval(tickVPNIcon, ICON_POLL_MS);
    }

    function init() {
        try { renderSidebar(); } catch (e) {}
        try { startVPNIconPoll(); } catch (e) {}
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }

    // The SPA may rerender the sidebar (e.g. on language switch); if
    // our entry gets blown away, re-insert. Debounced observer.
    if (typeof MutationObserver !== "undefined") {
        var pending = false;
        new MutationObserver(function () {
            if (pending) return;
            pending = true;
            setTimeout(function () {
                pending = false;
                if (!document.getElementById(SIDEBAR_ID)) renderSidebar();
                // SPA may also rerender the topology view, dropping
                // our class. Re-apply on the next probe cycle by
                // simply forcing a tick — cheap (<10ms image probe).
                tickVPNIcon();
            }, 250);
        }).observe(document.body, { childList: true, subtree: true });
    }
})();
