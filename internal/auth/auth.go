package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aviary/agent/internal/config"
	"github.com/aviary/agent/internal/token"
)

var (
	ErrInvalidToken     = token.ErrInvalidToken
	ErrExpiredToken     = token.ErrExpiredToken
	ErrInvalidSignature = token.ErrInvalidSignature
	ErrIPNotAllowed     = errors.New("IP not allowed")
	ErrMissingPublicKey = errors.New("panel public key not configured")
)

type Claims struct {
	PanelID   string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Nonce     string
}

type Authenticator struct {
	publicKey    ed25519.PublicKey
	tokenExpiry  time.Duration
	allowedIPs   map[string]bool
	allowedCIDRs []*net.IPNet
}

func NewAuthenticator(cfg *config.AuthConfig) (*Authenticator, error) {
	if cfg.PanelPublicKey == "" {
		return nil, ErrMissingPublicKey
	}

	keyBytes, err := base64.StdEncoding.DecodeString(cfg.PanelPublicKey)
	if err != nil {
		return nil, err
	}

	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key size")
	}

	allowedIPs := make(map[string]bool)
	var allowedCIDRs []*net.IPNet
	for _, ip := range cfg.AllowedIPs {
		if strings.Contains(ip, "/") {
			_, cidr, err := net.ParseCIDR(ip)
			if err == nil {
				allowedCIDRs = append(allowedCIDRs, cidr)
			}
		} else {
			allowedIPs[ip] = true
		}
	}

	return &Authenticator{
		publicKey:    keyBytes,
		tokenExpiry:  cfg.TokenExpiry,
		allowedIPs:   allowedIPs,
		allowedCIDRs: allowedCIDRs,
	}, nil
}

func (a *Authenticator) ValidateToken(tokenStr string) (*Claims, error) {
	tok, err := token.Parse(tokenStr, a.publicKey)
	if err != nil {
		return nil, err
	}

	return &Claims{
		PanelID:   tok.PanelID,
		IssuedAt:  time.Unix(tok.IssuedAt, 0),
		ExpiresAt: time.Unix(tok.ExpiresAt, 0),
		Nonce:     base64.RawURLEncoding.EncodeToString(tok.Nonce),
	}, nil
}

func (a *Authenticator) CheckIP(r *http.Request) error {
	ip := a.extractIP(r)
	if ip == "" {
		return ErrIPNotAllowed
	}

	if a.allowedIPs[ip] {
		return nil
	}

	for _, cidr := range a.allowedCIDRs {
		if cidr.Contains(net.ParseIP(ip)) {
			return nil
		}
	}

	return ErrIPNotAllowed
}

func (a *Authenticator) extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func GenerateToken(panelID string, privateKey ed25519.PrivateKey, expiry time.Duration) (string, error) {
	return token.Generate(panelID, privateKey, expiry)
}