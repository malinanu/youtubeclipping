// Package auth implements single-admin login with an HMAC-signed session cookie.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

const CookieName = "cf_session"

type Auth struct {
	Email        string
	PasswordHash []byte
	Secret       []byte
	TTL          time.Duration
	Secure       bool

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func New(email, hash string, secret []byte, ttl time.Duration, secure bool) *Auth {
	return &Auth{Email: strings.ToLower(email), PasswordHash: []byte(hash), Secret: secret, TTL: ttl, Secure: secure,
		limiters: map[string]*rate.Limiter{}}
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.Secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Issue returns a cookie value: base64(email|expiry).signature
func (a *Auth) Issue(now time.Time) string {
	payload := a.Email + "|" + strconv.FormatInt(now.Add(a.TTL).Unix(), 10)
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return enc + "." + a.sign(enc)
}

func (a *Auth) Verify(token string, now time.Time) bool {
	enc, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(a.sign(enc))) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	email, expS, ok := strings.Cut(string(raw), "|")
	if !ok || email != a.Email {
		return false
	}
	exp, err := strconv.ParseInt(expS, 10, 64)
	return err == nil && now.Unix() < exp
}

// CheckCredentials compares in constant-ish time.
func (a *Auth) CheckCredentials(email, password string) bool {
	emailOK := subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(email))), []byte(a.Email)) == 1
	pwErr := bcrypt.CompareHashAndPassword(a.PasswordHash, []byte(password))
	return emailOK && pwErr == nil
}

// Allow rate-limits login attempts per client IP: 5 burst, 1 per 12s.
func (a *Auth) Allow(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.limiters[ip]
	if !ok {
		if len(a.limiters) > 10000 {
			a.limiters = map[string]*rate.Limiter{}
		}
		l = rate.NewLimiter(rate.Every(12*time.Second), 5)
		a.limiters[ip] = l
	}
	return l.Allow()
}

func (a *Auth) SetCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieName, a.Issue(time.Now()), int(a.TTL.Seconds()), "/", "", a.Secure, true)
}

func (a *Auth) ClearCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(CookieName, "", -1, "/", "", a.Secure, true)
}

// Middleware rejects unauthenticated requests and enforces a CSRF header on
// state-changing methods (custom headers cannot be sent cross-site without CORS).
func (a *Auth) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok, err := c.Cookie(CookieName)
		if err != nil || !a.Verify(tok, time.Now()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if RequiresCSRF(c.Request.Method) && c.GetHeader("X-Requested-With") != "clipforge" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing csrf header"})
			return
		}
		c.Next()
	}
}

func RequiresCSRF(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
