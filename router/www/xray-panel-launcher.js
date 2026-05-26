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

    // Sidebar dot states. When our XRAY tunnel is active, the GL.iNet
    // SPA's own state machine still shows green "is-active" dots next
    // to VPN sub-items it cares about (VPN Dashboard, WireGuard
    // Client, etc.) — irrelevant noise when the actual outbound is
    // ours. Hide those, recolor the parent VPN section indicator
    // blue, and put one blue dot on our XRAY launcher entry.
    var SIDEBAR_CSS_ID = "xray-vpn-sidebar-style";
    var SIDEBAR_MODE_CLASS = "xray-vpn-mode";          // on <body> while active
    var SIDEBAR_TITLE_MARK = "xray-vpn-title";         // on the top-level VPN <el-submenu__title>
    var SIDEBAR_DOT_CLASS = "xray-vpn-sidebar-dot";    // our injected blue dot

    function injectSidebarCSS() {
        if (document.getElementById(SIDEBAR_CSS_ID)) return;
        var style = document.createElement("style");
        style.id = SIDEBAR_CSS_ID;
        var c = ACTIVE_COLOR;
        // Selectors:
        // 1. Hide stock dots inside any element under the sidebar VPN
        //    section (data-testid prefix navbar.vpn.* covers the
        //    submenu items; the marker class covers the top-level
        //    title that doesn't always carry that data-testid).
        // 2. Style our injected dot — small blue circle, inline so
        //    it sits next to the menu label without disrupting the
        //    row layout.
        style.textContent =
            // Hide every stock .status-badge anywhere under the
            // sidebar VPN section. Three selectors cover three nest
            // shapes: the top-level <li class="el-submenu"
            // data-testid="navbar.vpn.button"> (its own title +
            // submenu items), explicit .el-submenu__title scoped by
            // our marker class, and submenu items by their data-testid.
            "body." + SIDEBAR_MODE_CLASS + " li.el-submenu[data-testid^='navbar.vpn'] .status-badge, " +
            "body." + SIDEBAR_MODE_CLASS + " li.el-menu-item[data-testid^='navbar.vpn'] .status-badge, " +
            "body." + SIDEBAR_MODE_CLASS + " ." + SIDEBAR_TITLE_MARK + " .status-badge { " +
                "display: none !important; " +
            "} " +
            "." + SIDEBAR_DOT_CLASS + " { " +
                "display: inline-block; " +
                "width: 8px; height: 8px; " +
                "border-radius: 50%; " +
                "background-color: " + c + " !important; " +
                "margin-left: 8px; " +
                "vertical-align: middle; " +
                "flex-shrink: 0; " +
            "}";
        document.head.appendChild(style);
    }

    function findTopLevelVPNTitle() {
        // The top-level VPN sidebar header is <div class="el-submenu__title">
        // whose direct label leaf reads "VPN" (as opposed to "VPN
        // Dashboard" / "VPN XRAY", which are submenu *items*, not titles).
        var titles = document.querySelectorAll(".el-submenu__title");
        for (var i = 0; i < titles.length; i++) {
            var t = titles[i];
            var leaves = t.querySelectorAll("span, em, b");
            for (var j = 0; j < leaves.length; j++) {
                var leaf = leaves[j];
                if (leaf.children.length > 0) continue;
                if ((leaf.textContent || "").trim() === "VPN") return t;
            }
        }
        return null;
    }

    function makeBlueDot() {
        var d = document.createElement("span");
        d.className = SIDEBAR_DOT_CLASS;
        return d;
    }

    function ensureDotIn(host) {
        if (!host) return;
        if (host.querySelector("." + SIDEBAR_DOT_CLASS)) return;
        host.appendChild(makeBlueDot());
    }

    // Insert the blue dot at the same DOM position the stock
    // .status-badge used to sit — right after the .menu-title span,
    // before the .el-submenu__icon-arrow chevron. The title is
    // display:flex; a plain appendChild lands the dot AFTER the
    // chevron, which on a narrow sidebar pushes it out of view.
    function ensureDotInTitle(title) {
        if (!title) return;
        if (title.querySelector("." + SIDEBAR_DOT_CLASS)) return;
        var dot = makeBlueDot();
        var arrow = title.querySelector(".el-submenu__icon-arrow");
        if (arrow && arrow.parentElement === title) {
            title.insertBefore(dot, arrow);
            return;
        }
        var menuTitle = title.querySelector(".menu-title, .uppercase");
        if (menuTitle && menuTitle.parentElement === title) {
            title.insertBefore(dot, menuTitle.nextSibling);
            return;
        }
        title.appendChild(dot);
    }

    function applySidebarDots(active) {
        injectSidebarCSS();
        if (active) {
            document.body.classList.add(SIDEBAR_MODE_CLASS);
            var title = findTopLevelVPNTitle();
            if (title) {
                title.classList.add(SIDEBAR_TITLE_MARK);
                ensureDotInTitle(title);
            }
            ensureDotIn(document.getElementById(SIDEBAR_ID));
        } else {
            document.body.classList.remove(SIDEBAR_MODE_CLASS);
            var marked = document.querySelectorAll("." + SIDEBAR_TITLE_MARK);
            for (var i = 0; i < marked.length; i++) {
                marked[i].classList.remove(SIDEBAR_TITLE_MARK);
            }
            var dots = document.querySelectorAll("." + SIDEBAR_DOT_CLASS);
            for (var j = 0; j < dots.length; j++) dots[j].remove();
        }
    }

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
        // Sync sidebar dot state on every tick so SPA repaints don't
        // un-paint our marker — class on body + class on title +
        // injected dots are all idempotent re-applies.
        applySidebarDots(active);

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

    // ── dashboard injection — XRAY tunnel card on #/vpndashboard ────
    //
    // Gated behind injection.mode (panel.yaml). Off by default. When
    // enabled, clones the native single-mode tunnel card structure,
    // swaps labels/handlers to talk to :9092, and appends it as a
    // second card inside .single-mode-wrapper so it sits below the
    // stock WG/OVPN tunnel.
    //
    // Mode is fetched once at page load via /api/launcher-config (a
    // PUBLIC endpoint on :9092 — no creds prompt). Switching mode in
    // panel.yaml + restarting the panel takes effect on the next
    // browser refresh; no JS redeploy needed.
    var CARD_ID = "xray-vpn-card";
    var DRAWER_ID = "xray-vpn-drawer";
    var DASH_HASH_RX = /^#\/vpndashboard/i;
    var DASH_POLL_MS = 5000;
    var MODE = "legacy";          // overwritten by fetchMode() if successful
    var dashTimer = null;
    var dashState = null;         // last /api/state payload
    var dashProfiles = null;      // last /api/profiles payload

    function apiOrigin() {
        return location.protocol.replace("https:", "http:") +
               "//" + location.hostname + ":" + PORT;
    }

    function apiFetch(path, opts) {
        opts = opts || {};
        opts.credentials = "include";   // send cached basic-auth
        opts.cache = "no-store";
        return fetch(apiOrigin() + path, opts);
    }

    function fetchMode() {
        // Promise resolving to mode string. Network/auth/timeout errors
        // all degrade to "legacy" — safest fallback (= current behaviour
        // without the dashboard injection).
        return new Promise(function (resolve) {
            var done = false;
            function finish(m) {
                if (done) return;
                done = true;
                resolve(m || "legacy");
            }
            setTimeout(function () { finish("legacy"); }, 3500);
            apiFetch("/api/launcher-config")
                .then(function (r) { return r.ok ? r.json() : null; })
                .then(function (j) { finish(j && j.mode); })
                .catch(function () { finish("legacy"); });
        });
    }

    function onDashboardRoute() {
        return DASH_HASH_RX.test(location.hash);
    }

    // Strip ids, inline event handlers, and aria descriptors from a
    // cloned subtree. We KEEP class names AND data-v-* scope attrs —
    // Vue 2 templates compile @click into addEventListener on the
    // specific element, and listeners do NOT travel through
    // cloneNode, so leaving data-v-* in place is safe AND lets the
    // Vue scoped CSS rules apply to our clone (the whole point of
    // cloning instead of rebuilding from scratch). The id removal
    // also matters: ids are unique-by-spec, and the cloned DOM
    // breaks the SPA's $refs targeting if it carries duplicates.
    function purgeIds(node) {
        if (!node || node.nodeType !== 1) return;
        if (node.id) node.removeAttribute("id");
        ["onclick", "onmousedown", "onmouseup", "tabindex", "aria-describedby"].forEach(function (a) {
            if (node.hasAttribute(a)) node.removeAttribute(a);
        });
        for (var i = 0; i < node.children.length; i++) {
            purgeIds(node.children[i]);
        }
    }

    function findDashWrapper() {
        return document.querySelector(".single-mode-wrapper");
    }

    function findNativeCard(wrapper) {
        if (!wrapper) return null;
        return wrapper.querySelector(".gl-card-wrapper.single-mode-card");
    }

    function findNativeDrawer() {
        // Exclude our own injected clone (also carries .setting-drawer)
        // so this returns the template, not the copy.
        var els = document.querySelectorAll(".el-drawer__wrapper.setting-drawer");
        for (var i = 0; i < els.length; i++) {
            if (!els[i].hasAttribute("data-xray-drawer")) return els[i];
        }
        return null;
    }

    function setText(el, txt) {
        if (!el) return;
        // Replace text nodes, leave child elements intact (some labels
        // are wrapped with icons / tooltip spans).
        for (var n = el.firstChild; n; n = n.nextSibling) {
            if (n.nodeType === 3) {
                n.nodeValue = txt;
                return;
            }
        }
        el.textContent = txt;
    }

    // Build XRAY card by cloning native single-mode-card and rewriting
    // labels + handlers. Idempotent — if card already exists, just
    // updates its contents from state.
    function renderDashCard() {
        var wrapper = findDashWrapper();
        if (!wrapper) return false;
        // Look up by data-attribute, not id: purgeIds() strips ids
        // (intentional — we don't want to inherit native scoped ids),
        // so the only durable marker is our own data-xray-card attr.
        var existing = wrapper.querySelector("[data-xray-card='1']");
        if (existing) {
            updateDashCard(existing);
            return true;
        }
        var native = findNativeCard(wrapper);
        if (!native) return false;

        var clone = native.cloneNode(true);
        // Purge first so the native id (if any) doesn't survive, THEN
        // attach our own marker. Doing it in the other order made
        // purgeIds strip our id and the observer kept re-mounting
        // because getElementById(CARD_ID) was always null.
        purgeIds(clone);
        clone.id = CARD_ID;
        clone.setAttribute("data-xray-card", "1");

        // 1. Replace "VPN Client" label with our name.
        var info = clone.querySelector(".info");
        if (info) {
            // The first text node inside .info is " VPN Client " — Vue
            // wraps it in a <span> sibling to the kill-switch-tag.
            var labelSpan = info.querySelector("span");
            setText(labelSpan, " XRAY (sing-box) ");
        }

        // 2. Type cell ("WireGuard" → "VLESS+Reality").
        var typeCells = clone.querySelectorAll("ul.infos.label-list li:not(.title-li) > div");
        if (typeCells.length >= 1) {
            setText(typeCells[0], "VLESS+Reality");
        }

        // 3. Settings cog → open our :9092 panel in a new tab for full
        //    profile management. Stub for Phase 1; killswitch toggle
        //    drawer is Phase 3.
        var cog = clone.querySelector(".icon-setting");
        if (cog) {
            cog.title = "Open XRAY panel (:" + PORT + ")";
            cog.style.cursor = "pointer";
            cog.addEventListener("click", function (e) {
                e.preventDefault();
                e.stopImmediatePropagation();
                e.stopPropagation();
                window.open(apiOrigin() + "/", "_blank", "noopener");
            }, true);
        }

        // 4. Profile-row click → open our drawer. Attach handlers to
        //    every plausible click target (.file-info parent, .via-info,
        //    .via-label, the arrow icon) so a click anywhere along the
        //    row triggers ours regardless of where the SPA originally
        //    bound. Each handler hard-stops propagation.
        var rowTargets = [
            clone.querySelector(".file-info"),
            clone.querySelector(".via-info"),
            clone.querySelector(".via-label"),
            clone.querySelector(".file-info .iconfont.icon-toggle"),
        ];
        var openHandler = function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            e.stopPropagation();
            openXrayDrawer();
        };
        rowTargets.forEach(function (el) {
            if (el) el.addEventListener("click", openHandler, true);
        });

        // 5. ON/OFF gl-switch click → POST /api/service start|stop.
        var swEl = clone.querySelector(".gl-switch");
        if (swEl) {
            swEl.style.cursor = "pointer";
            swEl.addEventListener("click", function (e) {
                e.preventDefault();
                e.stopImmediatePropagation();
                e.stopPropagation();
                toggleService();
            }, true);
        }

        // 6. Make the kill-switch tag interactive: click toggles
        //    POST /api/killswitch. Stock UI shows "Kill Switch" /
        //    "Failover" tag as a STATUS (you click the settings cog
        //    to flip it via a dialog). We collapse that into one
        //    direct click — same tag, drives behaviour. State (and
        //    label) is updated from dashState in updateDashCard.
        var ksTag = clone.querySelector(".kill-switch-tag");
        if (ksTag) {
            ksTag.style.cursor = "pointer";
            ksTag.title = "Click to toggle killswitch (XRAY)";
            ksTag.addEventListener("click", function (e) {
                e.preventDefault();
                e.stopImmediatePropagation();
                e.stopPropagation();
                toggleKillswitch();
            }, true);
        }

        // 6b. Side-switch selector: a wider gl-switch-style toggle
        //     with the two VPN names as labels instead of ON/OFF. Sits
        //     in the same spot as the old pill tag, next to Kill Switch.
        //     Click ↔ POST /api/side-switch (handles native↔xray VPN
        //     swap transactionally; see internal/http/side_switch.go).
        //     Visual state mirrors dashState.bind_switch in
        //     updateDashCard.
        if (ksTag && !clone.querySelector("[data-xray-bind-selector]")) {
            var bindSel = document.createElement("div");
            bindSel.setAttribute("data-xray-bind-selector", "1");
            bindSel.setAttribute("role", "switch");
            bindSel.title = "Click to swap active VPN (WireGuard ↔ XRAY) and bind the physical side switch";
            bindSel.style.cssText =
                "position:relative;display:inline-block;" +
                "width:140px;height:24px;border-radius:12px;" +
                "background:var(--text-weak,#888);" +
                "cursor:pointer;vertical-align:middle;" +
                "margin-left:8px;" +
                "transition:background-color .4s ease;" +
                "-webkit-user-select:none;user-select:none;";

            var bindThumb = document.createElement("span");
            bindThumb.setAttribute("data-xray-bind-thumb", "1");
            bindThumb.style.cssText =
                "position:absolute;top:2px;left:2px;" +
                "width:20px;height:20px;border-radius:50%;" +
                "background:#fff;box-shadow:0 1px 3px rgba(0,0,0,0.35);" +
                "transition:left .3s ease;";
            bindSel.appendChild(bindThumb);

            var bindLabel = document.createElement("span");
            bindLabel.setAttribute("data-xray-bind-label", "1");
            bindLabel.style.cssText =
                "position:absolute;top:50%;transform:translateY(-50%);" +
                "font-size:11px;font-weight:700;color:#fff;line-height:1;" +
                "pointer-events:none;white-space:nowrap;letter-spacing:0;";
            bindSel.appendChild(bindLabel);

            bindSel.addEventListener("click", function (e) {
                e.preventDefault();
                e.stopImmediatePropagation();
                e.stopPropagation();
                toggleSideSwitch();
            }, true);
            ksTag.parentNode.insertBefore(bindSel, ksTag.nextSibling);
        }

        // 6b. Strip inherited connected-state rows from the cloned
        //     native card. If the native WG card was connected at
        //     clone time, its .label-list has extra <li> rows
        //     (Server Address, Port, Traffic, Virtual IPs, View Log).
        //     They have no data-xray-extra marker so our usual cleanup
        //     in renderConnectedExtras wouldn't catch them — they'd
        //     sit there forever showing WG's data on our XRAY card.
        //     Keep only .title-li and the <li> that contains .file-info
        //     (the profile-picker row), wipe everything else.
        var ul = clone.querySelector("ul.infos.label-list");
        if (ul) {
            for (var li = ul.firstElementChild; li; ) {
                var next = li.nextElementSibling;
                if (!li.classList.contains("title-li") && !li.querySelector(".file-info")) {
                    li.remove();
                }
                li = next;
            }
        }

        // 7. Replace the WireGuard logo with a periodic-table-style
        //    element tile: atomic-number-corner + bold symbol + small
        //    "name" line. Self-contained inline SVG — no external
        //    assets, no impersonation of WireGuard branding.
        //
        //    Layout (viewBox 0..100):
        //        ┌────────────────┐
        //        │ 443      Re   │   ← "atomic number" + "atomic mass" hint
        //        │                │
        //        │     Xr         │   ← symbol (centred-ish)
        //        │                │
        //        │   Reality      │   ← element name
        //        └────────────────┘
        var logoImg = clone.querySelector(".common-logo");
        if (logoImg) {
            var svgNS = "http://www.w3.org/2000/svg";
            var svg = document.createElementNS(svgNS, "svg");
            svg.setAttribute("viewBox", "0 0 100 100");
            svg.setAttribute("class", "common-logo");
            // Match native logo size: original <img> had no inline
            // width/height, sized by `.common-logo` CSS class (~34px
            // square in their stylesheet) and the parent
            // `.via-logo-wrapper` box. SVG without explicit width
            // attrs defaults to 300×150 — way too big. Copy the
            // native dimensions by reading the parent's box; fall
            // back to a sane default.
            var nativeBox = logoImg.getBoundingClientRect();
            var size = Math.round(Math.max(28, Math.min(nativeBox.width || 34, nativeBox.height || 34)));
            svg.setAttribute("width", size);
            svg.setAttribute("height", size);
            svg.style.cssText = "border-radius:4px;display:block";

            // Background — gradient navy → indigo, matches the
            // panel's "tech / cryptographic" feel.
            var defs = document.createElementNS(svgNS, "defs");
            var grad = document.createElementNS(svgNS, "linearGradient");
            grad.setAttribute("id", "xrayElGrad");
            grad.setAttribute("x1", "0"); grad.setAttribute("y1", "0");
            grad.setAttribute("x2", "0"); grad.setAttribute("y2", "1");
            var s1 = document.createElementNS(svgNS, "stop");
            s1.setAttribute("offset", "0%"); s1.setAttribute("stop-color", "#3b4ec9");
            var s2 = document.createElementNS(svgNS, "stop");
            s2.setAttribute("offset", "100%"); s2.setAttribute("stop-color", "#1e2a6b");
            grad.appendChild(s1); grad.appendChild(s2);
            defs.appendChild(grad);
            svg.appendChild(defs);

            var bg = document.createElementNS(svgNS, "rect");
            bg.setAttribute("x", "2"); bg.setAttribute("y", "2");
            bg.setAttribute("width", "96"); bg.setAttribute("height", "96");
            bg.setAttribute("rx", "8");
            bg.setAttribute("fill", "url(#xrayElGrad)");
            svg.appendChild(bg);

            // Atomic number (top-left) — "443" is the Reality default
            // TLS port; a fun nod for those who'll notice.
            var atomic = document.createElementNS(svgNS, "text");
            atomic.setAttribute("x", "10"); atomic.setAttribute("y", "22");
            atomic.setAttribute("fill", "#dfe5ff");
            atomic.setAttribute("font-family", "monospace");
            atomic.setAttribute("font-size", "13");
            atomic.setAttribute("font-weight", "600");
            atomic.textContent = "443";
            svg.appendChild(atomic);

            // Top-right hint — "TLS" group classification.
            var grp = document.createElementNS(svgNS, "text");
            grp.setAttribute("x", "90"); grp.setAttribute("y", "22");
            grp.setAttribute("fill", "#dfe5ff");
            grp.setAttribute("text-anchor", "end");
            grp.setAttribute("font-family", "monospace");
            grp.setAttribute("font-size", "10");
            grp.setAttribute("font-weight", "500");
            grp.setAttribute("opacity", "0.7");
            grp.textContent = "TLS";
            svg.appendChild(grp);

            // Symbol — bold "Xr", roughly centred.
            var sym = document.createElementNS(svgNS, "text");
            sym.setAttribute("x", "50"); sym.setAttribute("y", "66");
            sym.setAttribute("fill", "#ffffff");
            sym.setAttribute("text-anchor", "middle");
            sym.setAttribute("font-family", "Helvetica, Arial, sans-serif");
            sym.setAttribute("font-size", "38");
            sym.setAttribute("font-weight", "700");
            sym.setAttribute("letter-spacing", "-1");
            sym.textContent = "Xr";
            svg.appendChild(sym);

            // Name — "Reality", small caps at the bottom.
            var nm = document.createElementNS(svgNS, "text");
            nm.setAttribute("x", "50"); nm.setAttribute("y", "86");
            nm.setAttribute("fill", "#dfe5ff");
            nm.setAttribute("text-anchor", "middle");
            nm.setAttribute("font-family", "Helvetica, Arial, sans-serif");
            nm.setAttribute("font-size", "11");
            nm.setAttribute("font-weight", "500");
            nm.setAttribute("letter-spacing", "1.5");
            nm.textContent = "REALITY";
            svg.appendChild(nm);

            logoImg.parentNode.replaceChild(svg, logoImg);
        }

        // Mount: append AFTER the native card so the layout reads
        // "native first, XRAY second".
        wrapper.appendChild(clone);

        // Initial state paint (state will be re-pulled by next tick).
        updateDashCard(clone);
        return true;
    }

    function updateDashCard(card) {
        if (!card) return;
        // Before first /api/state lands, dashState is null. Render
        // the card structure but mark it loading — avoids the brief
        // "(no profile selected)" / "OFF" flash that misled the user
        // into thinking their tunnel was actually down on every
        // hashchange re-entry.
        var loading = !dashState;
        if (loading) {
            card.classList.remove("is-connected");
            card.classList.remove("is-disconnected");
            var lbl = card.querySelector(".via-label");
            if (lbl) setText(lbl, " Loading… ");
            // Don't paint connected-extras when we don't know state
            renderConnectedExtras(card, false);
            return;
        }

        // Connected class drives the card colour (native CSS). Use our
        // sing-box running status.
        var running = dashState && dashState.service && dashState.service.value === true;
        card.classList.toggle("is-connected", running);
        card.classList.toggle("is-disconnected", !running);

        // Kill-switch tag text + state. Stock convention:
        //   killswitch ON  → "Kill Switch" (red/highlighted)
        //   killswitch OFF → "Failover"    (muted/gray)
        // We don't have the native CSS rule to drive the colour
        // automatically; nudge it with inline style so the visual
        // state is unambiguous to the user.
        var ksTag = card.querySelector(".kill-switch-tag:not([data-xray-bind-tag])");
        if (ksTag) {
            var ksOn = !!(dashState && dashState.killswitch && dashState.killswitch.value === true);
            // Pad with non-breaking spaces so the badge has the same
            // optical width across both states (avoids the row
            // jumping when toggling).
            ksTag.textContent = ksOn ? " Kill Switch " : " Failover ";
            // The stock CSS for `.kill-switch-tag` already gives a
            // pill shape; we just tweak background/colour by state.
            if (ksOn) {
                ksTag.style.background = "#e07c5b";
                ksTag.style.color = "#fff";
                ksTag.style.borderColor = "#e07c5b";
            } else {
                ksTag.style.background = "transparent";
                ksTag.style.color = "var(--text-weak,#888)";
                ksTag.style.borderColor = "var(--text-weak,#888)";
            }
        }

        // Side-switch selector: slides between WireGuard VPN (OFF state,
        // thumb left, gray bg) and XRAY VPN (ON state, thumb right,
        // blue bg). Mirrors the visual language of the native gl-switch
        // ON/OFF toggle but with full VPN-name labels.
        var bindSelEl = card.querySelector("[data-xray-bind-selector]");
        if (bindSelEl) {
            var bindOn = !!(dashState && dashState.bind_switch && dashState.bind_switch.value === true);
            var bindThumbEl = bindSelEl.querySelector("[data-xray-bind-thumb]");
            var bindLabelEl = bindSelEl.querySelector("[data-xray-bind-label]");
            if (bindOn) {
                bindSelEl.style.backgroundColor = "#5272f7";
                if (bindThumbEl) bindThumbEl.style.left = "118px";  // 140 - 20 - 2
                if (bindLabelEl) {
                    bindLabelEl.textContent = "XRAY VPN";
                    bindLabelEl.style.left  = "10px";
                    bindLabelEl.style.right = "auto";
                }
            } else {
                bindSelEl.style.backgroundColor = "var(--text-weak,#888)";
                if (bindThumbEl) bindThumbEl.style.left = "2px";
                if (bindLabelEl) {
                    bindLabelEl.textContent = "WireGuard VPN";
                    bindLabelEl.style.left  = "auto";
                    bindLabelEl.style.right = "10px";
                }
            }
        }

        // Switch element. Native .gl-switch is a Vue-driven Element-UI
        // component with two .msg spans inside. Vue swaps each span's
        // class between hidden and visible variants based on its
        // reactive `checked` prop:
        //   first  span: .on-msg (hidden) ↔ .is-on  (visible "ON")
        //   second span: .off-msg (hidden) ↔ .is-off (visible "OFF")
        // Driving the underlying prop from outside Vue is brittle, so
        // we hide ALL .msg spans (regardless of which variant class is
        // current) and overlay our own text. .is-checked on the parent
        // still drives background colour + thumb position from CSS, no
        // Vue state needed.
        var sw = card.querySelector(".gl-switch");
        if (sw) {
            sw.classList.toggle("is-checked", !!running);
            // Catch every .msg span — previously we only matched .on-msg
            // / .is-off / .off-msg, missing the .is-on variant. That left
            // native "ON" text visible at left:9px alongside our overlay,
            // which read as a doubled/garbled letter. Also force
            // visibility:hidden + font-size:0 so any CSS transition on
            // opacity can't briefly expose the native text mid-flip.
            var msgs = sw.querySelectorAll(".msg");
            for (var mi = 0; mi < msgs.length; mi++) {
                var m = msgs[mi];
                m.style.setProperty("opacity",    "0",      "important");
                m.style.setProperty("visibility", "hidden", "important");
                m.style.setProperty("font-size",  "0",      "important");
            }
            // Our own label, mounted once and updated on state. Match
            // native font (weight 700, size 12px) so the visual feel
            // is identical to the stock toggle.
            var ownLabel = sw.querySelector("[data-xray-sw-label]");
            if (!ownLabel) {
                ownLabel = document.createElement("span");
                ownLabel.setAttribute("data-xray-sw-label", "1");
                ownLabel.style.cssText =
                    "position:absolute;top:50%;transform:translateY(-50%);" +
                    "font-size:12px;font-weight:700;color:#fff;" +
                    "pointer-events:none;line-height:1;letter-spacing:0;" +
                    "-webkit-user-select:none;user-select:none;";
                sw.appendChild(ownLabel);
            }
            // Native positions: ON at left:9px translateY(-50%); OFF at
            // left:51px translate(-100%,-50%) — which equals right:5px in
            // a 56px-wide container. Using right:9px earlier put OFF text
            // 4px closer to the thumb than native, leaving only ~2px gap;
            // the thumb's drop shadow then clipped the leading "O".
            if (running) {
                ownLabel.textContent = "ON";
                ownLabel.style.left  = "9px";
                ownLabel.style.right = "auto";
            } else {
                ownLabel.textContent = "OFF";
                ownLabel.style.left  = "auto";
                ownLabel.style.right = "5px";
            }
        }

        // Profile label: prefer active profile's name; fall back to
        // "(no profile selected)".
        var label = card.querySelector(".via-label");
        if (label) {
            var name = "(no profile selected)";
            if (dashState && dashState.active_profile && dashState.active_profile.value) {
                name = dashState.active_profile.value.name || name;
            }
            setText(label, " " + name + " ");
        }

        // Connected-state extras: server / port / traffic / virtual IP /
        // View Log row. Stock card extends .main with these when its
        // tunnel is up; we mirror that to keep visual parity.
        renderConnectedExtras(card, running);
    }

    // Inject Server Address, Server Listen Port, Traffic, Virtual IP
    // and View Log rows into our XRAY card's label-list when running,
    // remove them when stopped. Idempotent — re-runs on every poll.
    function renderConnectedExtras(card, running) {
        var ul = card.querySelector("ul.infos.label-list");
        if (!ul) return;
        // Track our rows by data-xray-extra="<key>"; remove any first.
        var existing = ul.querySelectorAll("[data-xray-extra]");
        existing.forEach(function (n) { n.remove(); });
        if (!running) return;

        var ap = dashState && dashState.active_profile && dashState.active_profile.value;
        if (!ap) return;

        var rows = [
            ["server",   "Server Address",      ap.server || "—"],
            ["port",     "Server Listen Port",  ap.port || "—"],
            ["traffic",  "Traffic Statistics",  formatTraffic()],
            ["tunip",    "Client Virtual IP (IPv4)", "172.19.0.1/30"],
            ["exitip",   "Exit IP",             liveExitIP || "checking…"],
        ];
        rows.forEach(function (r) {
            var li = document.createElement("li");
            li.setAttribute("data-xray-extra", r[0]);
            // Match the native two-column structure (label | value)
            var dLabel = document.createElement("div");
            dLabel.textContent = r[1];
            var dVal = document.createElement("div");
            dVal.textContent = String(r[2]);
            // Stock label rows have data-v-* attrs that bring scoped
            // CSS (font, padding, line height). Copy them from the
            // existing "title-li" row's children so our rows match.
            var titleRow = ul.querySelector(".title-li");
            if (titleRow && titleRow.children.length >= 2) {
                copyDataVAttrs(titleRow, li);
                copyDataVAttrs(titleRow.children[0], dLabel);
                copyDataVAttrs(titleRow.children[1], dVal);
            }
            li.appendChild(dLabel);
            li.appendChild(dVal);
            ul.appendChild(li);
        });

        // View Log row — single-column, right-aligned, clickable link
        // that opens a modal tailing /var/log/sing-box.log via :9092.
        var logLi = document.createElement("li");
        logLi.setAttribute("data-xray-extra", "viewlog");
        logLi.style.cssText = "text-align:right;cursor:pointer;color:#5272f7";
        var link = document.createElement("span");
        link.textContent = "View Log";
        link.style.cssText = "color:#5272f7;cursor:pointer";
        link.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            e.stopPropagation();
            openXrayLogDrawer();
        }, true);
        logLi.appendChild(link);
        ul.appendChild(logLi);
    }

    function copyDataVAttrs(from, to) {
        if (!from || !to) return;
        for (var i = 0; i < from.attributes.length; i++) {
            var a = from.attributes[i];
            if (a.name.indexOf("data-v-") === 0) to.setAttribute(a.name, a.value);
        }
    }

    // Traffic stats — cached, refreshed on each pollLive tick. Empty
    // until first sample.
    var liveTraffic = {up: 0, down: 0, upRate: 0, downRate: 0};
    function formatTraffic() {
        return "↑ " + formatBytes(liveTraffic.up) + " / ↓ " + formatBytes(liveTraffic.down);
    }
    function formatBytes(n) {
        if (n == null || isNaN(n) || n < 0) return "—";
        if (n < 1024) return n + " B";
        if (n < 1024*1024) return (n/1024).toFixed(2) + " KB";
        if (n < 1024*1024*1024) return (n/(1024*1024)).toFixed(2) + " MB";
        return (n/(1024*1024*1024)).toFixed(2) + " GB";
    }

    function toggleService() {
        var running = dashState && dashState.service && dashState.service.value === true;
        var action = running ? "stop" : "start";
        try { console.log("[xray-panel] POST /api/service", action); } catch (e) {}

        // Show transitional banner; close it on success or replace
        // with an error banner on failure. Banner lifecycle is bound
        // to this specific action — concurrent toggles just refresh it.
        var banner = showXrayBanner(
            action === "start" ? "Starting XRAY tunnel…" : "Stopping XRAY tunnel…",
            "progress");

        // Chain: (optional) stop native VPN clients → POST /api/service.
        // Mutual exclusion: when we go ON, the stock WG/OVPN umbrella
        // service goes OFF (in case the user had it running). Going OFF
        // doesn't touch native VPN clients.
        // On XRAY start: stop native WG/OVPN (mutex).
        // On XRAY stop: restore whatever native rules we previously
        // disabled, so the user's pre-XRAY VPN selection comes back
        // instead of staying stuck in enabled=0 limbo.
        var pre;
        if (action === "start") {
            pre = apiFetch("/api/native-vpn/stop", {method: "POST", body: ""})
                .then(function () { nudgeStockSPA(); })
                .catch(function () {});
            lastXrayOnAt = Date.now();   // for two-way mutex grace window
        } else {
            pre = Promise.resolve();
        }

        pre.then(function () {
            return apiFetch("/api/service", {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: JSON.stringify({action: action})
            });
        }).then(function (r) {
            return r.json().then(function (j) {
                return {ok: r.ok, status: r.status, body: j};
            }, function () {
                return {ok: r.ok, status: r.status, body: null};
            });
        }).then(function (res) {
            try { console.log("[xray-panel] /api/service →", res); } catch (e) {}
            if (res.ok) {
                if (action === "start") {
                    // Bypass the panel's 3s /api/state cache: a probe
                    // taken right after start may still see the
                    // pre-start "stopped" snapshot. Wait ~2s, hit /api/state
                    // again, and if the service is still off we know the
                    // init script silently returned 0 without bringing
                    // sing-box up (e.g. config validation passed but the
                    // process exited immediately). That mismatch is much
                    // worse than a "Failed to start" message because the
                    // UI happily shows ON while traffic falls through to
                    // the WAN bypass — surface it loudly.
                    updateXrayBanner(banner, "Verifying tunnel…", "progress");
                    setTimeout(function () {
                        apiFetch("/api/state")
                            .then(function (r) { return r.ok ? r.json() : null; })
                            .then(function (st) {
                                var up = st && st.service && st.service.value === true;
                                if (up) {
                                    updateXrayBanner(banner, "XRAY tunnel started ✓", "success");
                                    setTimeout(function () { hideXrayBanner(banner); }, 2400);
                                } else {
                                    updateXrayBanner(banner,
                                        "Started, but sing-box is not running. Check /var/log/sing-box.log (open :"+ PORT +" → Logs).",
                                        "error");
                                    setTimeout(function () { hideXrayBanner(banner); }, 8000);
                                }
                                if (st) dashState = st;
                                renderDashCard();
                            })
                            .catch(function () {
                                hideXrayBanner(banner);
                            });
                    }, 2000);
                } else {
                    // XRAY just went OFF — restore any native WG/OVPN
                    // rules we previously disabled so the user's
                    // pre-XRAY tunnel comes back without manual UI work.
                    // Best-effort: don't gate the success banner on it.
                    apiFetch("/api/native-vpn/restore", {method: "POST", body: ""})
                        .then(function () { nudgeStockSPA(); })
                        .catch(function () {});
                    updateXrayBanner(banner, "XRAY stopped — native WG/OVPN restoring", "success");
                    setTimeout(function () { hideXrayBanner(banner); }, 3200);
                }
            } else {
                var err = (res.body && res.body.error) || ("HTTP " + res.status);
                updateXrayBanner(banner, "Could not " + action + ": " + err, "error");
                setTimeout(function () { hideXrayBanner(banner); }, 5600);
            }
            pollDash(true);
        }).catch(function (err) {
            try { console.warn("[xray-panel] /api/service failed", err); } catch (e) {}
            updateXrayBanner(banner, "Network error: " + (err && err.message), "error");
            setTimeout(function () { hideXrayBanner(banner); }, 5600);
            pollDash(true);
        });
    }

    // Banner is a one-instance status strip that lives at the top-
    // centre of the screen. Returns the DOM node so the caller can
    // update / hide it as the action progresses. Multiple in-flight
    // actions all share the same banner — newer messages overwrite.
    var XRAY_BANNER_ID = "xray-action-banner";
    function showXrayBanner(text, kind) {
        var b = document.getElementById(XRAY_BANNER_ID);
        if (!b) {
            b = document.createElement("div");
            b.id = XRAY_BANNER_ID;
            b.style.cssText =
                "position:fixed;top:24px;left:50%;transform:translateX(-50%);" +
                "z-index:9999;padding:10px 18px;border-radius:6px;font-size:14px;" +
                "box-shadow:0 4px 16px rgba(0,0,0,0.35);max-width:80vw;" +
                "display:flex;align-items:center;gap:10px;" +
                "transition:opacity 200ms;";
            document.body.appendChild(b);
        }
        updateXrayBanner(b, text, kind);
        b.style.opacity = "1";
        return b;
    }
    function updateXrayBanner(b, text, kind) {
        if (!b) return;
        var bg = "#2a3045";
        if (kind === "progress") bg = "#1f2a52";
        if (kind === "success")  bg = "#1e3d2c";
        if (kind === "error")    bg = "#3a2828";
        b.style.background = bg;
        b.style.color = "#fff";
        // Build content: optional spinner + text
        b.innerHTML = "";
        if (kind === "progress") {
            var sp = document.createElement("span");
            sp.style.cssText =
                "display:inline-block;width:14px;height:14px;border-radius:50%;" +
                "border:2px solid rgba(255,255,255,0.3);border-top-color:#fff;" +
                "animation:xray-spin 800ms linear infinite;";
            b.appendChild(sp);
            // Inject keyframes once
            if (!document.getElementById("xray-spin-css")) {
                var s = document.createElement("style");
                s.id = "xray-spin-css";
                s.textContent = "@keyframes xray-spin{to{transform:rotate(360deg)}}";
                document.head.appendChild(s);
            }
        }
        var tx = document.createElement("span");
        tx.textContent = text;
        b.appendChild(tx);
    }
    function hideXrayBanner(b) {
        if (!b) return;
        b.style.opacity = "0";
        setTimeout(function () { if (b.parentNode) b.parentNode.removeChild(b); }, 250);
    }

    function toggleSideSwitch() {
        var on = !!(dashState && dashState.bind_switch && dashState.bind_switch.value === true);
        var nextOn = !on;
        try { console.log("[xray-panel] POST /api/side-switch on=", nextOn); } catch (e) {}
        var banner = showXrayBanner(
            nextOn ? "Binding side switch to XRAY…" : "Releasing side switch…",
            "progress");
        apiFetch("/api/side-switch", {
            method: "POST",
            headers: {"Content-Type": "application/json"},
            body: JSON.stringify({on: nextOn})
        }).then(function (r) {
            return r.json().then(function (j) { return {ok: r.ok, body: j}; },
                                  function ()  { return {ok: r.ok, body: null}; });
        }).then(function (res) {
            try { console.log("[xray-panel] /api/side-switch →", res); } catch (e) {}
            if (res.ok) {
                if (nextOn) {
                    updateXrayBanner(banner,
                        "Side switch bound to XRAY — flip the physical toggle to start/stop sing-box",
                        "success");
                } else {
                    updateXrayBanner(banner,
                        "Side switch released — native VPN handler back in control",
                        "success");
                }
                setTimeout(function () { hideXrayBanner(banner); }, 3600);
                nudgeStockSPA();
            } else {
                var err = (res.body && res.body.error) || "HTTP error";
                updateXrayBanner(banner, "Side switch change failed: " + err, "error");
                setTimeout(function () { hideXrayBanner(banner); }, 5600);
            }
            pollDash(true);
        }).catch(function (err) {
            updateXrayBanner(banner, "Network error: " + (err && err.message), "error");
            setTimeout(function () { hideXrayBanner(banner); }, 5600);
            pollDash(true);
        });
    }

    function toggleKillswitch() {
        var on = !!(dashState && dashState.killswitch && dashState.killswitch.value === true);
        var nextOn = !on;
        try { console.log("[xray-panel] POST /api/killswitch on=", nextOn); } catch (e) {}
        var banner = showXrayBanner(
            nextOn ? "Enabling killswitch…" : "Disabling killswitch…",
            "progress");
        apiFetch("/api/killswitch", {
            method: "POST",
            headers: {"Content-Type": "application/json"},
            body: JSON.stringify({on: nextOn})
        }).then(function (r) {
            return r.json().then(function (j) { return {ok: r.ok, body: j}; },
                                  function ()  { return {ok: r.ok, body: null}; });
        }).then(function (res) {
            try { console.log("[xray-panel] /api/killswitch →", res); } catch (e) {}
            if (res.ok) {
                updateXrayBanner(banner,
                    nextOn ? "Killswitch ON — LAN drops if tunnel down" : "Killswitch OFF — LAN falls back to direct WAN",
                    "success");
                setTimeout(function () { hideXrayBanner(banner); }, 3200);
            } else {
                var err = (res.body && res.body.error) || "HTTP error";
                updateXrayBanner(banner, "Killswitch change failed: " + err, "error");
                setTimeout(function () { hideXrayBanner(banner); }, 5600);
            }
            pollDash(true);
        }).catch(function (err) {
            updateXrayBanner(banner, "Network error: " + (err && err.message), "error");
            setTimeout(function () { hideXrayBanner(banner); }, 5600);
            pollDash(true);
        });
    }

    // Lightweight toast that floats over the dashboard for ~6s. Used
    // for action feedback (start blocked, fetch failed, etc.) without
    // pulling in Element-UI's $message machinery (which we'd have to
    // bind into Vue's runtime — not worth it for one message).
    function showXrayToast(text) {
        try { console.log("[xray-panel] toast:", text); } catch (e) {}
        var t = document.createElement("div");
        t.textContent = text;
        t.style.cssText =
            "position:fixed;top:24px;left:50%;transform:translateX(-50%);" +
            "z-index:9999;padding:10px 18px;border-radius:6px;" +
            "background:#3a2828;color:#fff;font-size:14px;" +
            "box-shadow:0 4px 16px rgba(0,0,0,0.3);" +
            "max-width:80vw;text-align:center;";
        document.body.appendChild(t);
        setTimeout(function () { t.style.opacity = "0"; t.style.transition = "opacity 400ms"; }, 5600);
        setTimeout(function () { if (t.parentNode) t.parentNode.removeChild(t); }, 6100);
    }

    // ── profile picker drawer ───────────────────────────────────────
    //
    // Built from scratch (NOT cloned from native Element-UI <el-drawer>).
    // Earlier we tried cloning — too many failure modes: Element-UI's
    // transition machinery on a detached clone, Vue's v-if lazy-rendering
    // means template descendants may not exist, etc. A standalone
    // sliding panel is simpler, fully controlled, and visually distinct
    // enough that the user knows it's "ours".
    function findXrayDrawer() {
        return document.querySelector("[data-xray-drawer='1']");
    }

    function buildXrayDrawer() {
        var existing = findXrayDrawer();
        if (existing) return existing;

        var d = document.createElement("div");
        d.id = DRAWER_ID;
        d.setAttribute("data-xray-drawer", "1");
        d.style.cssText =
            "position:fixed;top:0;right:0;bottom:0;width:min(420px,90vw);" +
            "z-index:2010;display:none;flex-direction:column;" +
            "background:var(--background-card,#0e1c33);color:var(--text-regular,#e6ebf4);" +
            "border-left:1px solid var(--border,#1c2640);" +
            "box-shadow:-8px 0 32px rgba(0,0,0,0.5);" +
            "transform:translateX(0);transition:transform 200ms ease-out;" +
            "font-family:inherit;";

        // Backdrop (click outside closes). Lives separately so we can
        // fade it independently from the slide.
        var bd = document.createElement("div");
        bd.setAttribute("data-xray-backdrop", "1");
        bd.style.cssText =
            "position:fixed;top:0;left:0;right:0;bottom:0;" +
            "z-index:2009;background:rgba(0,0,0,0.4);display:none;";
        bd.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            closeXrayDrawer();
        }, true);

        // Header
        var header = document.createElement("div");
        header.style.cssText =
            "padding:18px 20px;font-size:18px;font-weight:600;" +
            "border-bottom:1px solid var(--border,#1c2640);" +
            "display:flex;align-items:center;justify-content:space-between;";
        var title = document.createElement("span");
        title.textContent = "XRAY — Select Profile";
        var closeBtn = document.createElement("span");
        closeBtn.textContent = "✕";
        closeBtn.style.cssText = "cursor:pointer;color:var(--text-weak,#888);font-size:18px;padding:4px 8px;";
        closeBtn.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            closeXrayDrawer();
        }, true);
        header.appendChild(title);
        header.appendChild(closeBtn);

        // Scrollable body — this is where the list goes
        var body = document.createElement("div");
        body.setAttribute("data-xray-list", "1");
        body.style.cssText = "flex:1;overflow-y:auto;padding:12px 0;";

        // Footer with Cancel / Apply
        var footer = document.createElement("div");
        footer.style.cssText =
            "padding:16px 20px;border-top:1px solid var(--border,#1c2640);" +
            "display:flex;gap:12px;justify-content:flex-end;";
        var cancelBtn = document.createElement("button");
        cancelBtn.textContent = "Cancel";
        cancelBtn.style.cssText =
            "padding:8px 22px;border-radius:999px;border:1px solid var(--border,#1c2640);" +
            "background:transparent;color:var(--text-regular,#e6ebf4);cursor:pointer;font-size:14px;";
        cancelBtn.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            closeXrayDrawer();
        }, true);
        var applyBtn = document.createElement("button");
        applyBtn.textContent = "Apply";
        applyBtn.style.cssText =
            "padding:8px 22px;border-radius:999px;border:0;" +
            "background:#5272f7;color:#fff;cursor:pointer;font-size:14px;font-weight:500;";
        applyBtn.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            applyXrayProfile();
        }, true);
        footer.appendChild(cancelBtn);
        footer.appendChild(applyBtn);

        d.appendChild(header);
        d.appendChild(body);
        d.appendChild(footer);

        document.body.appendChild(bd);
        document.body.appendChild(d);
        d._backdrop = bd;
        return d;
    }

    function openXrayDrawer() {
        var drawer = buildXrayDrawer();
        if (!drawer) return;
        showLoadingDrawer(drawer);
        drawer._backdrop.style.display = "block";
        drawer.style.display = "flex";
        apiFetch("/api/profiles")
            .then(function (r) {
                if (!r.ok) throw new Error("HTTP " + r.status);
                return r.json();
            })
            .then(function (j) {
                dashProfiles = j;
                populateXrayDrawer(drawer);
            })
            .catch(function (err) {
                showErrorDrawer(drawer, err && err.message);
            });
    }

    function closeXrayDrawer() {
        var drawer = findXrayDrawer();
        if (drawer) {
            drawer.style.display = "none";
            if (drawer._backdrop) drawer._backdrop.style.display = "none";
        }
    }

    function getDrawerList(drawer) {
        return drawer.querySelector("[data-xray-list='1']");
    }

    function clearDrawerList(drawer) {
        var list = getDrawerList(drawer);
        if (list) list.innerHTML = "";
        return list;
    }

    function showLoadingDrawer(drawer) {
        var list = clearDrawerList(drawer);
        if (!list) return;
        var ph = document.createElement("div");
        ph.style.cssText = "padding:16px 20px;color:var(--text-weak,#888)";
        ph.textContent = "Loading profiles…";
        list.appendChild(ph);
    }

    function showErrorDrawer(drawer, msg) {
        var list = clearDrawerList(drawer);
        if (!list) return;
        var ph = document.createElement("div");
        ph.style.cssText = "padding:16px 20px;color:#e57c5b;line-height:1.5";
        var safeMsg = String(msg || "unknown error");
        ph.innerHTML = "Failed to load profiles: " + safeMsg.replace(/</g,"&lt;") +
            "<br><br><a href=\"" + apiOrigin() + "/\" target=\"_blank\" rel=\"noopener\" " +
            "style=\"color:#5272f7;text-decoration:underline\">Open XRAY panel to log in</a>" +
            "<br><br><span style=\"font-size:12px;color:#888\">Tip: after logging in there, Chrome will cache the credentials for cross-origin XHR. Then come back here and re-open this drawer.</span>";
        list.appendChild(ph);
    }

    function populateXrayDrawer(drawer) {
        var list = clearDrawerList(drawer);
        if (!list) return;
        var profiles = (dashProfiles && dashProfiles.profiles) || [];
        var activeID = (dashProfiles && dashProfiles.active_id) || null;
        if (profiles.length === 0) {
            var empty = document.createElement("div");
            empty.style.cssText = "padding:16px 20px;color:var(--text-weak, #888)";
            empty.innerHTML = "No profiles yet. <a href=\"" + apiOrigin() + "/\" target=\"_blank\" rel=\"noopener\" " +
                "style=\"color:#5272f7\">Open XRAY panel</a> to import a vless:// URL.";
            list.appendChild(empty);
            return;
        }
        profiles.forEach(function (p) {
            var row = document.createElement("div");
            var isActive = p.id === activeID;
            row.dataset.xrayId = p.id;
            row.style.cssText =
                "display:flex;align-items:center;gap:12px;" +
                "padding:14px 20px;margin:0 8px;border-radius:8px;cursor:pointer;" +
                "border:1px solid " + (isActive ? "#5272f7" : "transparent") + ";" +
                "background:" + (isActive ? "rgba(82,114,247,0.12)" : "transparent") + ";";
            // Radio dot
            var dot = document.createElement("span");
            dot.style.cssText =
                "flex:none;width:18px;height:18px;border-radius:50%;" +
                "border:2px solid " + (isActive ? "#5272f7" : "var(--text-weak,#888)") + ";" +
                "background:" + (isActive ? "radial-gradient(#5272f7 35%, transparent 36%)" : "transparent") + ";";
            // Text block
            var txt = document.createElement("div");
            txt.style.cssText = "display:flex;flex-direction:column;gap:2px;flex:1;min-width:0;";
            var nm = document.createElement("span");
            nm.textContent = p.name || "(unnamed)";
            nm.style.cssText = "font-size:14px;color:var(--text-regular,#e6ebf4);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;";
            var loc = document.createElement("span");
            loc.textContent = (p.server || "?") + ":" + (p.port || "?");
            loc.style.cssText = "font-size:12px;color:var(--text-weak,#888);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;";
            txt.appendChild(nm);
            txt.appendChild(loc);
            row.appendChild(dot);
            row.appendChild(txt);

            row.addEventListener("click", function (e) {
                e.preventDefault();
                e.stopImmediatePropagation();
                // Mark selection (visually + DOM marker that
                // survives any race with a concurrent re-populate).
                var rows = list.querySelectorAll("[data-xray-id]");
                rows.forEach(function (r) {
                    r.removeAttribute("data-xray-id-selected");
                    r.style.borderColor = "transparent";
                    r.style.background = "transparent";
                    var d = r.firstElementChild;
                    if (d) {
                        d.style.borderColor = "var(--text-weak,#888)";
                        d.style.background = "transparent";
                    }
                });
                row.setAttribute("data-xray-id-selected", "1");
                row.style.borderColor = "#5272f7";
                row.style.background = "rgba(82,114,247,0.12)";
                dot.style.borderColor = "#5272f7";
                dot.style.background = "radial-gradient(#5272f7 35%, transparent 36%)";
                drawer._selectedId = p.id;
                try { console.log("[xray-panel] drawer select id=", p.id, "name=", p.name); } catch (e2) {}
            }, true);
            // Pre-mark the initially-active row so DOM agrees with
            // drawer._selectedId from the start.
            if (isActive) row.setAttribute("data-xray-id-selected", "1");
            list.appendChild(row);
        });
        drawer._selectedId = activeID;
    }

    function applyXrayProfile() {
        var drawer = findXrayDrawer();
        if (!drawer) return;
        // Prefer the DOM as the source of truth — the currently
        // visually-selected row has `data-xray-id-selected="1"`.
        // Falling back to drawer._selectedId in case the DOM marker
        // got lost (e.g. drawer was re-populated during selection).
        var list = getDrawerList(drawer);
        var selectedRow = list ? list.querySelector("[data-xray-id-selected='1']") : null;
        var id = (selectedRow && selectedRow.dataset.xrayId) || drawer._selectedId;
        try { console.log("[xray-panel] applyXrayProfile id=", id, "dom-sel=", selectedRow && selectedRow.dataset.xrayId, "drawer._sel=", drawer._selectedId); } catch (e) {}
        if (!id) { closeXrayDrawer(); return; }
        var current = (dashProfiles && dashProfiles.active_id) || null;
        // Pick the profile name for the banner — match list visually.
        var profName = "profile";
        if (dashProfiles && dashProfiles.profiles) {
            for (var i = 0; i < dashProfiles.profiles.length; i++) {
                if (dashProfiles.profiles[i].id === id) {
                    profName = dashProfiles.profiles[i].name || profName;
                    break;
                }
            }
        }
        closeXrayDrawer();
        if (id === current) { pollDash(true); return; }
        var banner = showXrayBanner("Activating " + profName + "…", "progress");
        apiFetch("/api/profiles/" + encodeURIComponent(id) + "/activate", {
            method: "POST",
            headers: {"Content-Type": "application/json"},
            body: "{}"
        }).then(function (r) {
            return r.json().then(function (j) {
                return {ok: r.ok, status: r.status, body: j};
            }, function () {
                return {ok: r.ok, status: r.status, body: null};
            });
        }).then(function (res) {
            try { console.log("[xray-panel] /api/profiles/activate →", res); } catch (e) {}
            if (res.ok) {
                // Surface what kind of switch happened (instant clash
                // selector vs full reload vs first start) — useful
                // for triaging "why did it stall for 5 sec".
                var hint = "";
                if (res.body) {
                    if (res.body.switched) hint = " (instant switch)";
                    else if (res.body.reloaded) hint = " (reloaded)";
                    else if (res.body.started) hint = " (service started)";
                    else if (res.body.pending_switch) hint = " (pending — flip side switch ON)";
                }
                updateXrayBanner(banner, "Activated " + profName + hint + " ✓", "success");
                setTimeout(function () { hideXrayBanner(banner); }, 2800);
            } else {
                var err = (res.body && res.body.error) || ("HTTP " + res.status);
                updateXrayBanner(banner, "Activation failed: " + err, "error");
                setTimeout(function () { hideXrayBanner(banner); }, 5600);
            }
            pollDash(true);
        }).catch(function (err) {
            updateXrayBanner(banner, "Network error: " + (err && err.message), "error");
            setTimeout(function () { hideXrayBanner(banner); }, 5600);
            pollDash(true);
        });
    }

    // ── Sing-box log drawer ─────────────────────────────────────────
    //
    // Mirrors the native WG "View Log" link → opens a side panel and
    // tails /var/log/sing-box.log via :9092 /api/logs every 2s while
    // visible. Auto-scrolls to the bottom unless the user has
    // scrolled up (keep their scroll position so they can read).
    var LOG_DRAWER_ID = "xray-vpn-log-drawer";
    var logPollTimer = null;
    function buildXrayLogDrawer() {
        var ex = document.getElementById(LOG_DRAWER_ID);
        if (ex) return ex;

        var d = document.createElement("div");
        d.id = LOG_DRAWER_ID;
        d.setAttribute("data-xray-log-drawer", "1");
        d.style.cssText =
            "position:fixed;top:0;right:0;bottom:0;width:min(720px,95vw);" +
            "z-index:2020;display:none;flex-direction:column;" +
            "background:var(--background-card,#0e1c33);color:var(--text-regular,#e6ebf4);" +
            "border-left:1px solid var(--border,#1c2640);" +
            "box-shadow:-8px 0 32px rgba(0,0,0,0.5);" +
            "font-family:inherit;";

        var bd = document.createElement("div");
        bd.setAttribute("data-xray-log-backdrop", "1");
        bd.style.cssText =
            "position:fixed;inset:0;z-index:2019;background:rgba(0,0,0,0.4);display:none;";
        bd.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            closeXrayLogDrawer();
        }, true);

        var header = document.createElement("div");
        header.style.cssText =
            "padding:18px 20px;font-size:18px;font-weight:600;" +
            "border-bottom:1px solid var(--border,#1c2640);" +
            "display:flex;align-items:center;justify-content:space-between;";
        var title = document.createElement("span");
        title.textContent = "XRAY — Sing-box log (live)";
        var closeBtn = document.createElement("span");
        closeBtn.textContent = "✕";
        closeBtn.style.cssText = "cursor:pointer;color:var(--text-weak,#888);font-size:18px;padding:4px 8px;";
        closeBtn.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopImmediatePropagation();
            closeXrayLogDrawer();
        }, true);
        header.appendChild(title);
        header.appendChild(closeBtn);

        var body = document.createElement("pre");
        body.setAttribute("data-xray-log-body", "1");
        body.style.cssText =
            "flex:1;overflow:auto;margin:0;padding:12px 16px;" +
            "font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;" +
            "font-size:12px;line-height:1.5;white-space:pre;color:#cbd2e1;";
        body.textContent = "Loading…";

        d.appendChild(header);
        d.appendChild(body);
        document.body.appendChild(bd);
        document.body.appendChild(d);
        d._backdrop = bd;
        return d;
    }

    function openXrayLogDrawer() {
        var d = buildXrayLogDrawer();
        d._backdrop.style.display = "block";
        d.style.display = "flex";
        tickLog();
        if (logPollTimer) clearInterval(logPollTimer);
        logPollTimer = setInterval(tickLog, 2000);
    }

    function closeXrayLogDrawer() {
        var d = document.getElementById(LOG_DRAWER_ID);
        if (!d) return;
        d.style.display = "none";
        if (d._backdrop) d._backdrop.style.display = "none";
        if (logPollTimer) { clearInterval(logPollTimer); logPollTimer = null; }
    }

    function tickLog() {
        var d = document.getElementById(LOG_DRAWER_ID);
        if (!d || d.style.display === "none") return;
        var body = d.querySelector("[data-xray-log-body='1']");
        if (!body) return;
        apiFetch("/api/logs?lines=400")
            .then(function (r) { return r.ok ? r.json() : null; })
            .then(function (j) {
                if (!j) {
                    body.textContent = "Could not fetch logs (check :"+ PORT +" panel auth).";
                    return;
                }
                var lines = (j && j.lines) || [];
                var txt = lines.join("\n");
                // Detect user scroll: only auto-scroll to bottom if
                // the user was already AT (or very near) the bottom
                // before we updated. Otherwise leave their position.
                var atBottom = (body.scrollHeight - body.scrollTop - body.clientHeight) < 24;
                body.textContent = txt || "(empty log)";
                if (atBottom) body.scrollTop = body.scrollHeight;
            })
            .catch(function () {
                body.textContent = "Network error fetching logs.";
            });
    }

    // ── dashboard polling ──────────────────────────────────────────
    var dashPolling = false;
    // Track previous native-VPN-active value so we can detect the
    // false→true transition (= "user just turned on a native client").
    // Reset on hash navigation to avoid stale comparisons.
    var lastNativeActive = null;
    var lastXrayOnAt = 0;
    function maybeStopOnNativeUp() {
        var nv = dashState && dashState.native_vpn_active && dashState.native_vpn_active.value;
        var prev = lastNativeActive;
        lastNativeActive = !!nv;
        if (prev !== false || nv !== true) return;  // only act on false→true
        var running = dashState && dashState.service && dashState.service.value === true;
        if (!running) return;
        // Avoid fighting our own /api/service start that fired moments
        // ago. The grace window covers /api/native-vpn/stop + restart
        // settle time, which on this hardware is ~1-3s.
        if (Date.now() - lastXrayOnAt < 6000) return;
        try { console.log("[xray-panel] native VPN came up → stopping our sing-box"); } catch (e) {}
        var banner = showXrayBanner(
            "Native WG/OVPN turned on — stopping XRAY (one tunnel at a time)",
            "progress");
        apiFetch("/api/service", {
            method: "POST",
            headers: {"Content-Type": "application/json"},
            body: JSON.stringify({action: "stop"})
        }).then(function () {
            updateXrayBanner(banner, "XRAY stopped (native VPN now active)", "success");
            setTimeout(function () { hideXrayBanner(banner); }, 3200);
            pollDash(true);
        }).catch(function () {
            updateXrayBanner(banner, "Could not stop XRAY automatically — check :"+ PORT, "error");
            setTimeout(function () { hideXrayBanner(banner); }, 5600);
        });
    }

    // Best-effort kick to make the GL.iNet stock SPA re-fetch its
    // VPN state without a hard refresh. The native dashboard polls
    // ~every 10s on its own; many Vue components also re-fetch on
    // window.focus / document.visibilitychange. Firing these
    // synthetically tends to trigger an earlier poll. Falls back to
    // their natural cadence if the listeners aren't hooked.
    function nudgeStockSPA() {
        try {
            window.dispatchEvent(new Event("focus"));
            document.dispatchEvent(new Event("visibilitychange"));
        } catch (e) {}
    }

    function isDrawerOpen() {
        var d = findXrayDrawer();
        return d && d.style.display !== "none";
    }

    function pollDash(force) {
        if (!onDashboardRoute()) return;
        if (dashPolling && !force) return;
        dashPolling = true;
        // Only fetch /api/live when sing-box is actually up — clash-API
        // is gated on the running daemon, and the call costs a few
        // shell-outs. When the card is in "disconnected" state we don't
        // surface the traffic row anyway.
        var liveFetch;
        var running = dashState && dashState.service && dashState.service.value === true;
        if (running) {
            liveFetch = apiFetch("/api/live").then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; });
        } else {
            liveFetch = Promise.resolve(null);
        }
        Promise.all([
            apiFetch("/api/state").then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; }),
            (dashProfiles ? Promise.resolve(dashProfiles)
                          : apiFetch("/api/profiles").then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; })),
            liveFetch
        ]).then(function (vals) {
            // Detect active-profile change to invalidate browserExitIP.
            // When the user activates a different profile, the previous
            // value is stale by definition — force an immediate re-fetch
            // by zeroing the dedup timestamp.
            var newActiveID = vals[0] && vals[0].active_profile && vals[0].active_profile.value && vals[0].active_profile.value.id;
            var newRunning  = vals[0] && vals[0].service        && vals[0].service.value === true;
            if (newActiveID && newActiveID !== lastActiveID) {
                browserExitIP   = "";
                browserExitIPAt = 0;
                lastActiveID    = newActiveID;
            }
            // Service flip true↔false also invalidates: stopping the
            // tunnel routes traffic via WAN, so the next ipify hit
            // should reflect that.
            if (lastServiceRunning !== null && newRunning !== lastServiceRunning) {
                browserExitIP   = "";
                browserExitIPAt = 0;
            }
            lastServiceRunning = newRunning;

            if (vals[0]) dashState = vals[0];
            if (vals[1]) dashProfiles = vals[1];
            if (vals[2] && vals[2].traffic && vals[2].traffic.ok && vals[2].traffic.value) {
                var t = vals[2].traffic.value;
                liveTraffic.up = t.up_total || 0;
                liveTraffic.down = t.down_total || 0;
                liveTraffic.upRate = t.up_rate || 0;
                liveTraffic.downRate = t.down_rate || 0;
            }
            if (vals[2] && vals[2].exit_ip && vals[2].exit_ip.ok) {
                // backend returns exit_ip.value as {ip, fetched_at, age_sec} —
                // grab just the IP string, fall back to whatever's there
                // if the shape was different.
                //
                // NOTE: the backend poller fetches via the router's default
                // route (panel-originated traffic doesn't match the
                // iif=br-lan rule), so this value reflects the ROUTER's WAN
                // egress, not what LAN clients see through the tunnel. We
                // overwrite it with browserExitIP below when the
                // browser-side check has produced a result.
                var ev = vals[2].exit_ip.value;
                if (ev && typeof ev === "object") liveExitIP = ev.ip || "";
                else liveExitIP = ev || "";
            }
            // Prefer the browser-side check — the browser is on the LAN
            // (192.168.200.x for beryl), so its packets do route via
            // sing-tun. That makes the answer the "real" tunnel exit IP
            // from the LAN-client point of view.
            if (browserExitIP) liveExitIP = browserExitIP;
            // Kick the browser-side fetch — internal 30s dedup keeps
            // this cheap on every poll cycle.
            if (window.fetch) refreshBrowserExitIP();
            // Two-way mutex: if a native VPN tunnel just came up while
            // our XRAY is also running, stop ours. Acts only on the
            // false→true transition so a steady-state "native up, ours
            // down" doesn't trigger anything. Suppressed for ~5 seconds
            // after WE turned ours on, so the brief overlap window
            // between /api/native-vpn/stop + /api/service start doesn't
            // toggle us back off.
            try { maybeStopOnNativeUp(); } catch (e) {}
            renderDashCard();
            // Do NOT re-populate the drawer while it's open — a poll
            // that lands mid-selection would wipe `data-xray-id-selected`
            // and the user's click would silently revert to the
            // pre-existing active.
        }).finally(function () {
            dashPolling = false;
        });
    }
    var liveExitIP = "";
    // browserExitIP is the IP api.ipify.org sees the BROWSER as. Because
    // the browser is on beryl's LAN, its traffic routes via sing-tun when
    // a tunnel is up — so this value reflects the actual user-facing exit
    // IP, not the router's WAN egress.
    var browserExitIP = "";
    var browserExitIPAt = 0;
    var lastActiveID = null;
    var lastServiceRunning = null;
    function refreshBrowserExitIP() {
        // Stale check + de-dup: at most one fetch in flight, refresh
        // every 30s. AbortController caps the wait so a hung tunnel
        // doesn't pile up XHRs.
        if (Date.now() - browserExitIPAt < 30000) return;
        browserExitIPAt = Date.now();
        var ctrl = (typeof AbortController === "function") ? new AbortController() : null;
        var timer = ctrl ? setTimeout(function () { ctrl.abort(); }, 6000) : 0;
        fetch("https://api.ipify.org?format=text", {
            signal: ctrl ? ctrl.signal : undefined,
            cache: "no-store",
            referrerPolicy: "no-referrer",
        }).then(function (r) {
            if (timer) clearTimeout(timer);
            if (!r.ok) return null;
            return r.text();
        }).then(function (txt) {
            if (!txt) return;
            txt = txt.trim();
            // Tiny sanity check — IPv4 dotted or v6 colon-y
            if (/^[0-9a-f.:]{3,}$/i.test(txt)) {
                browserExitIP = txt;
            }
        }).catch(function () {
            // Network error or abort. Don't clear cached value — the
            // user keeps seeing the last successful answer until the
            // next attempt succeeds.
            if (timer) clearTimeout(timer);
        });
    }
    // Kick off on load and then on every dashboard poll cycle. The
    // 30s dedup inside refreshBrowserExitIP makes the per-poll call
    // a near-noop except on the 30s boundary.
    if (window.fetch) refreshBrowserExitIP();

    function stopDashTimer() {
        if (dashTimer) { clearInterval(dashTimer); dashTimer = null; }
    }

    function startDashTimer() {
        stopDashTimer();
        dashTimer = setInterval(pollDash, DASH_POLL_MS);
    }

    function tearDownDashCard() {
        var card = document.querySelector("[data-xray-card='1']");
        if (card && card.parentNode) card.parentNode.removeChild(card);
        var drawer = findXrayDrawer();
        if (drawer && drawer.parentNode) drawer.parentNode.removeChild(drawer);
        stopDashTimer();
    }

    function activateDashboardModule() {
        if (MODE !== "dashboard" && MODE !== "full") return;
        if (!onDashboardRoute()) { tearDownDashCard(); return; }
        pollDash(true);
        startDashTimer();
    }

    function init() {
        try { renderSidebar(); } catch (e) {}
        try { startVPNIconPoll(); } catch (e) {}
        // Mode fetch is async; start with legacy and upgrade if the
        // panel says so. No-op if /api/launcher-config is unreachable
        // (most likely cause: panel hasn't been deployed yet with the
        // new endpoint — keep working in legacy mode).
        fetchMode().then(function (m) {
            MODE = m;
            try { console.log("[xray-panel] injection.mode =", MODE); } catch (e) {}
            try { activateDashboardModule(); } catch (e) {}
        });
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
                // Dashboard card may also be wiped by Vue rerender or
                // by SPA navigation back-and-forth — re-mount if
                // missing and we're still on the right page. Match on
                // our data-attribute, not on id (purgeIds strips ids).
                //
                // Route through pollDash(true) instead of renderDashCard
                // directly, so the freshly-mounted card carries real
                // state from the first poll rather than the stale
                // snapshot left over from the previous visit. The poll
                // will renderDashCard itself when its fetch lands.
                if ((MODE === "dashboard" || MODE === "full") && onDashboardRoute()
                        && !document.querySelector("[data-xray-card='1']")) {
                    try { pollDash(true); } catch (e) {}
                }
            }, 250);
        }).observe(document.body, { childList: true, subtree: true });
    }

    // hashchange — SPA route navigation. Activate / tear down our
    // dashboard module accordingly. No-op when mode = legacy.
    window.addEventListener("hashchange", function () {
        try { activateDashboardModule(); } catch (e) {}
    });
})();
