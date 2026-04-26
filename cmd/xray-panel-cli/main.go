// xray-panel-cli is the web admin panel for the sing-box VPN client
// running on a GL.iNet Beryl router. It is the LAN-side counterpart of
// flint2-xray-web-console (which manages the server end).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"beryl-xray-web-console/internal/config"
	panelhttp "beryl-xray-web-console/internal/http"
	"beryl-xray-web-console/internal/service"
	"beryl-xray-web-console/internal/sysprobe"
	"beryl-xray-web-console/internal/ucitool"
)

func main() {
	configPath := flag.String("config", "/etc/xray-panel-cli/panel.yaml", "path to panel config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := panelhttp.CheckLANBind(cfg.Listen); err != nil {
		log.Fatalf("refusing to start: %v", err)
	}

	srv := &panelhttp.Server{
		Cfg: cfg,
		Service: &service.Manager{
			InitScript: cfg.SingBoxInit,
			// 45s: init's apply_lan_routing waits up to 15s for sing-tun
			// to appear after spawning sing-box, plus extra slack for the
			// procd handoff and iptables ops.
			Timeout: 45 * time.Second,
		},
		UCI:   &ucitool.Tool{Timeout: 5 * time.Second},
		Probe: &sysprobe.Probe{Timeout: 5 * time.Second},
	}

	// Force tcp4: Go's default "tcp" opens an AF_INET6 dual-stack
	// socket, and on the OpenWrt kernel running on Beryl, a dual-stack
	// socket bound to a wildcard answers IPv4 SYNs with SYN-ACKs that
	// route via lo with cleared headers — the LAN client never gets a
	// reply. AF_INET-only avoids that path.
	ln, err := net.Listen("tcp4", cfg.Listen)
	if err != nil {
		log.Fatalf("listen %s: %v", cfg.Listen, err)
	}

	httpSrv := &nethttp.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("xray-panel-cli listening on %s (tcp4)", cfg.Listen)
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
