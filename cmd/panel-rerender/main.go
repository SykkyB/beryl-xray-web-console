// panel-rerender is a maintenance helper: read profiles.json, render
// the corresponding sing-box config.json, sing-box check, atomic write,
// and (optionally) reload the service. Exists for the rare case where
// profiles.json was edited out-of-band, or to recover when an older
// panel build skipped a render after Add (the bug fixed in this commit).
//
//	./panel-rerender                               # uses /etc/xray-panel-cli/profiles.json + /etc/sing-box/config.json
//	./panel-rerender -active <profile-id>          # override selector default
//	./panel-rerender -no-reload                    # render only, don't restart sing-box
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"beryl-xray-web-console/internal/runner"
	"beryl-xray-web-console/internal/singbox"
	"beryl-xray-web-console/internal/store"
	"beryl-xray-web-console/internal/ucitool"
)

func main() {
	var (
		profilesPath = flag.String("profiles", "/etc/xray-panel-cli/profiles.json", "profiles store path")
		configPath   = flag.String("config", "/etc/sing-box/config.json", "sing-box config path")
		singboxBin   = flag.String("singbox", "/usr/bin/sing-box", "sing-box binary")
		activeID     = flag.String("active", "", "selector default (overrides UCI sing-box.config.active_profile; falls back to first profile)")
		noReload     = flag.Bool("no-reload", false, "render only, don't reload sing-box")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st := &store.Profiles{Path: *profilesPath}
	all, err := st.List()
	if err != nil {
		die("read profiles: %v", err)
	}
	if len(all) == 0 {
		die("no profiles in %s", *profilesPath)
	}

	if *activeID == "" {
		uci := &ucitool.Tool{Runner: runner.Exec{}}
		v, _ := uci.Get(ctx, "sing-box.config.active_profile")
		*activeID = v
	}
	if *activeID == "" {
		*activeID = all[0].ID
		fmt.Fprintf(os.Stderr, "active not set; defaulting to %s (%s)\n", *activeID, all[0].Name)
	}

	r := &singbox.Renderer{
		ConfigPath: *configPath,
		SingBoxBin: *singboxBin,
		Runner:     runner.Exec{},
	}
	if err := r.WriteAndCheck(ctx, all, *activeID); err != nil {
		die("render: %v", err)
	}
	fmt.Printf("rendered %s with %d outbounds, default=%s\n", *configPath, len(all), *activeID)

	if *noReload {
		return
	}
	if _, err := exec.LookPath("/etc/init.d/sing-box"); err != nil {
		fmt.Fprintf(os.Stderr, "no procd init at /etc/init.d/sing-box; render done, skipping reload\n")
		return
	}
	out, err := exec.CommandContext(ctx, "/etc/init.d/sing-box", "reload").CombinedOutput()
	if err != nil {
		die("reload: %v: %s", err, out)
	}
	fmt.Println("sing-box reloaded")
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
