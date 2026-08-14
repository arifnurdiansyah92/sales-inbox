package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
)

const (
	sessionAdminKey = "adminID"
	adminCacheTTL   = 60 * time.Second
	loginFailWindow = 15 * time.Minute
	loginMaxFails   = 5

	msgNotLoggedIn      = "Belum login"
	msgWrongCredentials = "Username atau password salah"
	msgTooManyAttempts  = "Terlalu banyak percobaan. Coba lagi nanti."
	msgOwnerOnly        = "Hanya owner yang bisa memutuskan koneksi WhatsApp"
)

// dummyHash keeps login timing roughly constant for unknown usernames: the
// argon2id compare always runs, against this hash when no admin row matches.
const dummyHash = `$argon2id$v=19$m=65536,t=1,p=4$3VO4NLuDT4URpJccoHlT3Q$8WnI9IBzFLCSnZxTHyfmKM9sEjYBIdg4tuLOgBEWJEc`

type cachedAdmin struct {
	admin Admin
	exp   time.Time
}

type Auth struct {
	st       *Store
	sessions *scs.SessionManager

	failMu   sync.Mutex
	failures map[string][]time.Time // "ip|username" -> recent failed logins

	cacheMu sync.Mutex
	cache   map[int64]cachedAdmin
}

func NewAuth(st *Store, frontendOrigin string) *Auth {
	sm := scs.New()
	sm.Store = postgresstore.New(st.db)
	sm.Lifetime = 30 * 24 * time.Hour
	sm.IdleTimeout = 7 * 24 * time.Hour
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Path = "/"
	sm.Cookie.Secure = strings.HasPrefix(frontendOrigin, "https")
	return &Auth{
		st:       st,
		sessions: sm,
		failures: make(map[string][]time.Time),
		cache:    make(map[int64]cachedAdmin),
	}
}

// adminCtxKey carries the authenticated Admin in the request context.
type adminCtxKey struct{}

func adminFrom(ctx context.Context) Admin {
	a, _ := ctx.Value(adminCtxKey{}).(Admin)
	return a
}

// Wrap chains session loading and authentication around the mux. /healthz
// bypasses both so a broken session store can never mask the health report.
func (au *Auth) Wrap(next http.Handler) http.Handler {
	protected := au.sessions.LoadAndSave(au.requireAuth(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

// requireAuth guards every /api/* route except /api/auth/login, plus /ws, and
// attaches the acting admin to the request context.
func (au *Auth) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/auth/login" || (!strings.HasPrefix(path, "/api/") && path != "/ws") {
			next.ServeHTTP(w, r)
			return
		}
		id := au.sessions.GetInt64(r.Context(), sessionAdminKey)
		if id == 0 {
			writeErr(w, http.StatusUnauthorized, msgNotLoggedIn)
			return
		}
		admin, ok := au.adminByID(r.Context(), id)
		if !ok {
			writeErr(w, http.StatusUnauthorized, msgNotLoggedIn)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminCtxKey{}, admin)))
	})
}

func (au *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Body tidak valid")
		return
	}
	body.Username = strings.TrimSpace(body.Username)

	key := clientIP(r) + "|" + body.Username
	if au.tooManyFailures(key) {
		writeErr(w, http.StatusTooManyRequests, msgTooManyAttempts)
		return
	}

	admin, hash, found, err := au.st.GetAdminByUsername(r.Context(), body.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		hash = dummyHash
	}
	match, err := argon2id.ComparePasswordAndHash(body.Password, hash)
	if err != nil || !match || !found {
		au.recordFailure(key)
		writeErr(w, http.StatusUnauthorized, msgWrongCredentials)
		return
	}

	// New identity, new token (session fixation).
	if err := au.sessions.RenewToken(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	au.sessions.Put(r.Context(), sessionAdminKey, admin.ID)
	au.clearFailures(key)
	au.cachePut(admin)
	writeJSON(w, http.StatusOK, admin)
}

func (au *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := au.sessions.Destroy(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMe runs behind requireAuth, so the admin is always present.
func (au *Auth) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, adminFrom(r.Context()))
}

// adminByID loads the admin, via a small cache so requireAuth does not hit the
// DB on every request. Cached entries live at most adminCacheTTL.
func (au *Auth) adminByID(ctx context.Context, id int64) (Admin, bool) {
	now := time.Now()
	au.cacheMu.Lock()
	if e, ok := au.cache[id]; ok && now.Before(e.exp) {
		au.cacheMu.Unlock()
		return e.admin, true
	}
	au.cacheMu.Unlock()
	admin, found, err := au.st.GetAdminByID(ctx, id)
	if err != nil || !found {
		return Admin{}, false
	}
	au.cachePut(admin)
	return admin, true
}

func (au *Auth) cachePut(a Admin) {
	au.cacheMu.Lock()
	au.cache[a.ID] = cachedAdmin{admin: a, exp: time.Now().Add(adminCacheTTL)}
	au.cacheMu.Unlock()
}

func pruneFailures(times []time.Time, now time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if now.Sub(t) < loginFailWindow {
			out = append(out, t)
		}
	}
	return out
}

func (au *Auth) tooManyFailures(key string) bool {
	now := time.Now()
	au.failMu.Lock()
	defer au.failMu.Unlock()
	recent := pruneFailures(au.failures[key], now)
	if len(recent) == 0 {
		delete(au.failures, key)
	} else {
		au.failures[key] = recent
	}
	return len(recent) >= loginMaxFails
}

func (au *Auth) recordFailure(key string) {
	now := time.Now()
	au.failMu.Lock()
	defer au.failMu.Unlock()
	// Bound the map: drop fully-expired keys before adding a new one.
	if len(au.failures) >= 1000 {
		for k, times := range au.failures {
			if len(pruneFailures(times, now)) == 0 {
				delete(au.failures, k)
			}
		}
	}
	au.failures[key] = append(pruneFailures(au.failures[key], now), now)
}

func (au *Auth) clearFailures(key string) {
	au.failMu.Lock()
	delete(au.failures, key)
	au.failMu.Unlock()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// EnsureDefaultAdmin membuat akun default admin/admin (owner) saat belum ada
// admin sama sekali (instalasi baru). Password default WAJIB segera diganti.
func EnsureDefaultAdmin(ctx context.Context, st *Store) error {
	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins WHERE account_id=$1`, accountID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := argon2id.CreateHash("admin", argon2id.DefaultParams)
	if err != nil {
		return err
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO admins (account_id, username, name, password_hash, is_owner) VALUES ($1, 'admin', 'Admin', $2, TRUE)`,
		accountID, hash); err != nil {
		return err
	}
	log.Println("PERINGATAN: akun default admin/admin dibuat karena belum ada admin — segera ganti password-nya lewat cmd/admin")
	return nil
}
