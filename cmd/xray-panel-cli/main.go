// xray-panel-cli is the web admin panel for the sing-box VPN client
// running on a GL.iNet Beryl router. It is the LAN-side counterpart of
// flint2-xray-web-console (which manages the server end).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"beryl-xray-web-console/internal/config"
	panelhttp "beryl-xray-web-console/internal/http"
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

	srv := &panelhttp.Server{Cfg: cfg}

	httpSrv := &nethttp.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("xray-panel-cli listening on %s", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
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
