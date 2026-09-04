// Package auth implements optional WebUI password authentication.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const pbkdf2Iterations = 600000

var PublicPaths = map[string]bool{
	"/login": true, "/health": true, "/favicon.ico": true, "/sw.js": true,
	"/api/auth/login": true, "/api/auth/status": true,
	"/share": true, "/manifest.json": true, "/manifest.webmanifest": true,
}

var cookieNameRe = regexp.MustCompile(`^[-!#$%&'*+.^_` + "`" + `|~0-9A-Za-z]+$`)

// Config drives Auth.
type Config struct {
	Password     string
	PasswordHash string
	StateDir     string
	SessionTTL   time.Duration
	CookieName   string
}

// Auth holds authentication state.
type Auth struct {
	mu           sync.Mutex
	pbkdf2Key    []byte
	signingKey   []byte
	sessions     map[string]float64
	sessionsPath string
	passwordHash string
	enabled      bool
	SessionTTL   time.Duration
	CookieName   string
}

// New loads key/session files and configures optional auth.
func New(cfg Config) (*Auth, error) {
	a := &Auth{sessions: make(map[string]float64), enabled: cfg.Password != "" || cfg.PasswordHash != "", SessionTTL: cfg.SessionTTL, CookieName: "hermes_session"}
	if a.SessionTTL <= 0 {
		a.SessionTTL = 30 * 24 * time.Hour
	}
	if cfg.CookieName != "" && cookieNameRe.MatchString(cfg.CookieName) {
		a.CookieName = cfg.CookieName
	}
	if !a.enabled {
		return a, nil
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	var err error
	a.pbkdf2Key, err = loadOrCreateKey(filepath.Join(cfg.StateDir, ".pbkdf2_key"))
	if err != nil {
		return nil, err
	}
	a.signingKey, err = loadOrCreateKey(filepath.Join(cfg.StateDir, ".signing_key"))
	if err != nil {
		return nil, err
	}
	if cfg.Password != "" {
		a.passwordHash = hashPassword(cfg.Password, a.pbkdf2Key)
	} else {
		a.passwordHash = cfg.PasswordHash
	}
	a.sessionsPath = filepath.Join(cfg.StateDir, "sessions.json")
	if err := a.loadSessions(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Auth) Enabled() bool { return a.enabled }

func (a *Auth) VerifyPassword(plain string) bool {
	if !a.enabled {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(hashPassword(plain, a.pbkdf2Key)), []byte(a.passwordHash)) == 1 {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(hashPassword(plain, a.signingKey)), []byte(a.passwordHash)) == 1 {
		a.mu.Lock()
		a.passwordHash = hashPassword(plain, a.pbkdf2Key)
		a.mu.Unlock()
		return true
	}
	return false
}

func (a *Auth) CreateSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	a.mu.Lock()
	a.sessions[token] = float64(time.Now().Add(a.SessionTTL).Unix())
	a.mu.Unlock()
	if err := a.saveSessions(); err != nil {
		return "", err
	}
	return token + "." + a.sign(token), nil
}

func (a *Auth) VerifySession(value string) bool {
	i := strings.LastIndex(value, ".")
	if i <= 0 {
		return false
	}
	token, sig := value[:i], value[i+1:]
	full := a.sign(token)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(full)) != 1 && !(len(sig) == 32 && subtle.ConstantTimeCompare([]byte(sig), []byte(full[:32])) == 1) {
		return false
	}
	a.mu.Lock()
	expiry, ok := a.sessions[token]
	if ok && time.Now().Unix() <= int64(expiry) {
		a.sessions[token] = float64(time.Now().Add(a.SessionTTL).Unix())
		a.mu.Unlock()
		return true
	}
	if ok {
		delete(a.sessions, token)
	}
	a.mu.Unlock()
	if ok {
		_ = a.saveSessions()
	}
	return false
}

// InvalidateSession removes a session token (raw token, not the signed
// cookie value) and persists the session store.
func (a *Auth) InvalidateSession(value string) {
	i := strings.LastIndex(value, ".")
	if i <= 0 {
		return
	}
	token := value[:i]
	a.mu.Lock()
	_, ok := a.sessions[token]
	delete(a.sessions, token)
	a.mu.Unlock()
	if ok {
		_ = a.saveSessions()
	}
}

// ClearCookie expires the auth cookie.
func (a *Auth) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !a.enabled || PublicPaths[p] || strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/share/") || strings.HasPrefix(p, "/session/static/") || strings.HasPrefix(p, "/manifest") {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(a.CookieName)
		if err == nil && a.VerifySession(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Authentication required"}`))
			return
		}
		http.Redirect(w, r, "/login?next="+r.URL.RequestURI(), http.StatusFound)
	})
}

func (a *Auth) SetCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: a.CookieName, Value: value, Path: "/", MaxAge: int(a.SessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (a *Auth) sign(token string) string {
	mac := hmac.New(sha256.New, a.pbkdf2Key)
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Auth) loadSessions() error {
	raw, err := os.ReadFile(a.sessionsPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored map[string]float64
	if json.Unmarshal(raw, &stored) != nil {
		return nil
	}
	now := float64(time.Now().Unix())
	for token, expiry := range stored {
		if expiry > now {
			a.sessions[token] = expiry
		}
	}
	return nil
}

func (a *Auth) saveSessions() error {
	a.mu.Lock()
	raw, err := json.Marshal(a.sessions)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if a.sessionsPath == "" {
		return nil
	}
	tmp := a.sessionsPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.sessionsPath)
}

func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil && len(raw) >= 16 {
		return raw, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func hashPassword(password string, salt []byte) string {
	return hex.EncodeToString(pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, 32))
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	out := make([]byte, 0, keyLen)
	blocks := (keyLen + sha256.Size - 1) / sha256.Size
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
