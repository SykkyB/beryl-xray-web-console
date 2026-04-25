package http

import (
	"embed"
	"io/fs"
	nethttp "net/http"
)

//go:embed web
var webFS embed.FS

// registerUIRoutes mounts the embedded static web UI at /. The build is
// the GZ-friendly net/http file server, no fancy routing — when the
// frontend grows it can switch to a SPA strategy.
func registerUIRoutes(mux *nethttp.ServeMux) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// Should never happen — web/ is part of the binary.
		panic(err)
	}
	fileServer := nethttp.FileServer(nethttp.FS(sub))
	mux.Handle("GET /", fileServer)
}
