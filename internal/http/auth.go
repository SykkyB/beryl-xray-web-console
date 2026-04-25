package http

import (
	"crypto/subtle"
	nethttp "net/http"

	"golang.org/x/crypto/bcrypt"
)

// BasicAuth wraps h so every request must present HTTP Basic credentials
// matching user / bcryptHash. On mismatch we reply 401 with a WWW-
// Authenticate challenge so browsers prompt for a password.
//
// The username comparison uses subtle.ConstantTimeCompare to avoid
// leaking length via timing; bcrypt's own comparison is constant-time.
func BasicAuth(user, bcryptHash string, h nethttp.Handler) nethttp.Handler {
	userBytes := []byte(user)
	hashBytes := []byte(bcryptHash)
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		u, p, ok := r.BasicAuth()
		if !ok {
			unauthorized(w)
			return
		}
		if subtle.ConstantTimeCompare([]byte(u), userBytes) != 1 {
			// Run bcrypt regardless to keep timing roughly constant.
			_ = bcrypt.CompareHashAndPassword(hashBytes, []byte(p))
			unauthorized(w)
			return
		}
		if err := bcrypt.CompareHashAndPassword(hashBytes, []byte(p)); err != nil {
			unauthorized(w)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func unauthorized(w nethttp.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="xray-panel-cli"`)
	nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
}
