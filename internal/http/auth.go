package http

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	nethttp "net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// authCache memoises successful bcrypt validations. bcrypt at cost=12
// takes ~100–300ms on this aarch64 hardware; doing it on every AJAX
// poll + every EventSource reconnect spikes the CPU to >100%. The
// cache key is sha256(username:password) so the plaintext password
// never lives anywhere in memory after the initial validation.
//
// TTL is short enough that a password change (restart of the panel
// clears the in-memory cache) takes effect quickly even on a machine
// that has been running for a while.
type authCache struct {
	mu      sync.RWMutex
	entries map[string]time.Time
}

const authCacheTTL = 5 * time.Minute

func newAuthCache() *authCache {
	return &authCache{entries: make(map[string]time.Time)}
}

func (c *authCache) key(user, pass string) string {
	h := sha256.Sum256([]byte(user + "\x00" + pass))
	return hex.EncodeToString(h[:])
}

func (c *authCache) hit(user, pass string) bool {
	k := c.key(user, pass)
	c.mu.RLock()
	exp, ok := c.entries[k]
	c.mu.RUnlock()
	return ok && time.Now().Before(exp)
}

func (c *authCache) admit(user, pass string) {
	k := c.key(user, pass)
	c.mu.Lock()
	c.entries[k] = time.Now().Add(authCacheTTL)
	// Trim expired entries opportunistically — keeps the map bounded
	// without a sweep goroutine. Cheap because the map is tiny.
	if len(c.entries) > 32 {
		now := time.Now()
		for k2, exp := range c.entries {
			if now.After(exp) {
				delete(c.entries, k2)
			}
		}
	}
	c.mu.Unlock()
}

// BasicAuth wraps h so every request must present HTTP Basic credentials
// matching user / bcryptHash. On mismatch we reply 401 with a WWW-
// Authenticate challenge so browsers prompt for a password.
//
// Successful validations are memoised by an authCache so the heavy
// bcrypt comparison runs only once per (user, password) per ~5 min.
func BasicAuth(user, bcryptHash string, h nethttp.Handler) nethttp.Handler {
	userBytes := []byte(user)
	hashBytes := []byte(bcryptHash)
	cache := newAuthCache()
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
		// Fast path — cache hit.
		if cache.hit(u, p) {
			h.ServeHTTP(w, r)
			return
		}
		if err := bcrypt.CompareHashAndPassword(hashBytes, []byte(p)); err != nil {
			unauthorized(w)
			return
		}
		cache.admit(u, p)
		h.ServeHTTP(w, r)
	})
}

func unauthorized(w nethttp.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="xray-panel-cli"`)
	nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
}
