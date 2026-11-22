package auth

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestSessionRoundTrip(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	a := New("Admin@Example.com", string(hash), []byte(strings.Repeat("k", 32)), time.Hour, true)
	now := time.Now()
	tok := a.Issue(now)
	if !a.Verify(tok, now) {
		t.Fatal("valid token rejected")
	}
	if a.Verify(tok, now.Add(2*time.Hour)) {
		t.Fatal("expired token accepted")
	}
	if a.Verify(tok[:len(tok)-2]+"xx", now) {
		t.Fatal("tampered token accepted")
	}
	b := New("admin@example.com", string(hash), []byte(strings.Repeat("z", 32)), time.Hour, true)
	if b.Verify(tok, now) {
		t.Fatal("token from other secret accepted")
	}
	if !a.CheckCredentials(" admin@example.com", "s3cret") || a.CheckCredentials("admin@example.com", "nope") {
		t.Fatal("credential check wrong")
	}
}

func TestRateLimit(t *testing.T) {
	a := New("a@b.c", "", []byte("x"), time.Hour, false)
	for i := 0; i < 5; i++ {
		if !a.Allow("1.2.3.4") {
			t.Fatal("burst blocked early")
		}
	}
	if a.Allow("1.2.3.4") {
		t.Fatal("limit not enforced")
	}
	if !a.Allow("5.6.7.8") {
		t.Fatal("other IP blocked")
	}
}
