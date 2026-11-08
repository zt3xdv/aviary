package token

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestGenerateAndParse(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	tokStr, err := Generate("panel-1", priv, time.Hour)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	tok, err := Parse(tokStr, pub)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if tok.PanelID != "panel-1" {
		t.Errorf("PanelID: expected panel-1, got %s", tok.PanelID)
	}

	if tok.IssuedAt <= 0 || tok.ExpiresAt <= 0 {
		t.Errorf("Invalid timestamps: issued=%d, expires=%d", tok.IssuedAt, tok.ExpiresAt)
	}

	if len(tok.Nonce) != nonceSize {
		t.Errorf("Nonce size: expected %d, got %d", nonceSize, len(tok.Nonce))
	}
}

func TestParseExpired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	tokStr, err := Generate("panel-1", priv, -time.Hour)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	_, err = Parse(tokStr, pub)
	if !errors.Is(err, ErrExpiredToken) {
		t.Errorf("Expected ErrExpiredToken, got %v", err)
	}
}

func TestParseInvalidSignature(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	wrongPub, _, _ := ed25519.GenerateKey(nil)

	tokStr, err := Generate("panel-1", priv, time.Hour)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	_, err = Parse(tokStr, wrongPub)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("Expected ErrInvalidSignature, got %v", err)
	}
}

func TestParseInvalidVersion(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	tokStr, err := Generate("panel-1", priv, time.Hour)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	data, _ := base64.RawURLEncoding.DecodeString(tokStr)
	data[0] = 99

	_, err = Parse(base64.RawURLEncoding.EncodeToString(data), pub)
	if !errors.Is(err, ErrInvalidVersion) {
		t.Errorf("Expected ErrInvalidVersion, got %v", err)
	}
}

func TestParseInvalidLength(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)

	_, err := Parse("invalid", pub)
	if !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrInvalidLength) {
		t.Errorf("Expected ErrInvalidToken or ErrInvalidLength, got %v", err)
	}
}

func TestParseTamperedPayload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	tokStr, err := Generate("panel-1", priv, time.Hour)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	data, _ := base64.RawURLEncoding.DecodeString(tokStr)
	data[10] ^= 0xFF

	_, err = Parse(base64.RawURLEncoding.EncodeToString(data), pub)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("Expected ErrInvalidSignature, got %v", err)
	}
}

func TestPanelIDTooLong(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)

	longID := string(make([]byte, panelIDMaxLen+1))
	_, err := Generate(longID, priv, time.Hour)
	if err == nil {
		t.Error("Expected error for panel ID too long")
	}
}

func TestConstantTimeCompare(t *testing.T) {
	a := []byte{1, 2, 3, 4}
	b := []byte{1, 2, 3, 4}
	c := []byte{1, 2, 3, 5}

	if !ConstantTimeCompare(a, b) {
		t.Error("Expected equal")
	}
	if ConstantTimeCompare(a, c) {
		t.Error("Expected not equal")
	}
}