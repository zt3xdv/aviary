package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aviary/agent/internal/config"
)

func TestAuthenticator(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pubB64 := base64.StdEncoding.EncodeToString(pub)

	cfg := &config.AuthConfig{
		PanelPublicKey: pubB64,
		TokenExpiry:    time.Hour,
		AllowedIPs:     []string{"127.0.0.1", "10.0.0.0/8"},
	}

	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatal(err)
	}

	token, err := GenerateToken("test-panel", priv, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	claims, err := auth.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if claims.PanelID != "test-panel" {
		t.Errorf("expected panel-id test-panel, got %s", claims.PanelID)
	}

	expiredToken, err := GenerateToken("test-panel", priv, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.ValidateToken(expiredToken)
	if err != ErrExpiredToken {
		t.Errorf("expected ErrExpiredToken, got %v", err)
	}

	invalidToken := "invalid.token.here"
	_, err = auth.ValidateToken(invalidToken)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}

	wrongKeyPub, _, _ := ed25519.GenerateKey(rand.Reader)
	wrongToken, _ := GenerateToken("test-panel", ed25519.NewKeyFromSeed(wrongKeyPub[:32]), time.Hour)
	_, err = auth.ValidateToken(wrongToken)
	if err != ErrInvalidSignature {
		t.Errorf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestAuthenticatorIPCheck(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	cfg := &config.AuthConfig{
		PanelPublicKey: pubB64,
		TokenExpiry:    time.Hour,
		AllowedIPs:     []string{"127.0.0.1", "10.0.0.0/8"},
	}

	auth, _ := NewAuthenticator(cfg)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	if err := auth.CheckIP(req); err != nil {
		t.Errorf("127.0.0.1 should be allowed: %v", err)
	}

	req.RemoteAddr = "10.0.0.5:12345"
	if err := auth.CheckIP(req); err != nil {
		t.Errorf("10.0.0.5 should be allowed: %v", err)
	}

	req.RemoteAddr = "192.168.1.1:12345"
	if err := auth.CheckIP(req); err != ErrIPNotAllowed {
		t.Errorf("192.168.1.1 should be denied")
	}

	req.Header.Set("X-Forwarded-For", "10.0.0.100")
	req.RemoteAddr = "192.168.1.1:12345"
	if err := auth.CheckIP(req); err != nil {
		t.Errorf("X-Forwarded-For 10.0.0.100 should be allowed: %v", err)
	}

	req.Header.Set("X-Forwarded-For", "192.168.1.100")
	req.RemoteAddr = "192.168.1.1:12345"
	if err := auth.CheckIP(req); err != ErrIPNotAllowed {
		t.Errorf("X-Forwarded-For 192.168.1.100 should be denied")
	}
}

func TestAuthenticatorMissingPublicKey(t *testing.T) {
	cfg := &config.AuthConfig{
		PanelPublicKey: "",
		TokenExpiry:    time.Hour,
	}
	_, err := NewAuthenticator(cfg)
	if err != ErrMissingPublicKey {
		t.Errorf("expected ErrMissingPublicKey, got %v", err)
	}
}

func TestAuthMiddleware(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	cfg := &config.AuthConfig{
		PanelPublicKey: pubB64,
		TokenExpiry:    time.Hour,
		AllowedIPs:     []string{"127.0.0.1"},
	}

	auth, _ := NewAuthenticator(cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panelID := r.Header.Get("X-Panel-ID")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(panelID))
	})

	mux := http.NewServeMux()
	mux.Handle("/", authMiddleware(handler, auth))

	token, _ := GenerateToken("panel-1", priv, time.Hour)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "127.0.0.1:12345"

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() != "panel-1" {
		t.Errorf("expected panel-1, got %s", rr.Body.String())
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("Authorization", "")
	req2.RemoteAddr = "127.0.0.1:12345"
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req2)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing token")
	}

	req2.Header.Set("Authorization", "Bearer invalid")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req2)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token")
	}
}

func authMiddleware(next http.Handler, auth *Authenticator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		token := r.Header.Get("Authorization")
		if token == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		token = strings.TrimPrefix(token, "Bearer ")
		claims, err := auth.ValidateToken(token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		if err := auth.CheckIP(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		r.Header.Set("X-Panel-ID", claims.PanelID)
		next.ServeHTTP(w, r)
	})
}