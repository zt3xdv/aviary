package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"time"
)

var (
	ErrInvalidToken     = errors.New("invalid token")
	ErrExpiredToken     = errors.New("token expired")
	ErrInvalidSignature = errors.New("invalid signature")
	ErrInvalidVersion   = errors.New("unsupported token version")
	ErrInvalidLength    = errors.New("invalid token length")
)

const (
	tokenVersion   = 1
	nonceSize      = 16
	signatureSize  = ed25519.SignatureSize
	minTokenSize   = 1 + 8 + 8 + nonceSize + signatureSize
	panelIDMaxLen  = 64
)

type Token struct {
	PanelID   string
	IssuedAt  int64
	ExpiresAt int64
	Nonce     []byte
}

func Generate(panelID string, privateKey ed25519.PrivateKey, expiry time.Duration) (string, error) {
	if len(panelID) > panelIDMaxLen {
		return "", ErrInvalidToken
	}

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	now := time.Now().Unix()
	tok := Token{
		PanelID:   panelID,
		IssuedAt:  now,
		ExpiresAt: now + int64(expiry.Seconds()),
		Nonce:     nonce,
	}

	payload := tok.marshal()
	sig := ed25519.Sign(privateKey, payload)

	token := make([]byte, 1+len(payload)+signatureSize)
	token[0] = tokenVersion
	copy(token[1:], payload)
	copy(token[1+len(payload):], sig)

	return base64.RawURLEncoding.EncodeToString(token), nil
}

func Parse(tokenStr string, publicKey ed25519.PublicKey) (*Token, error) {
	data, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if len(data) < minTokenSize {
		return nil, ErrInvalidLength
	}

	version := data[0]
	if version != tokenVersion {
		return nil, ErrInvalidVersion
	}

	payload := data[1 : len(data)-signatureSize]
	sig := data[len(data)-signatureSize:]

	if !ed25519.Verify(publicKey, payload, sig) {
		return nil, ErrInvalidSignature
	}

	tok, err := unmarshal(payload)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if time.Now().Unix() > tok.ExpiresAt {
		return nil, ErrExpiredToken
	}

	return tok, nil
}

func (t *Token) marshal() []byte {
	panelIDBytes := []byte(t.PanelID)
	if len(panelIDBytes) > panelIDMaxLen {
		panelIDBytes = panelIDBytes[:panelIDMaxLen]
	}

	buf := make([]byte, 1+len(panelIDBytes)+8+8+nonceSize)
	buf[0] = byte(len(panelIDBytes))
	copy(buf[1:1+len(panelIDBytes)], panelIDBytes)

	offset := 1 + len(panelIDBytes)
	putUint64(buf[offset:], uint64(t.IssuedAt))
	offset += 8
	putUint64(buf[offset:], uint64(t.ExpiresAt))
	offset += 8
	copy(buf[offset:], t.Nonce)

	return buf
}

func unmarshal(data []byte) (*Token, error) {
	if len(data) < 1+1+8+8+nonceSize {
		return nil, ErrInvalidToken
	}

	panelIDLen := int(data[0])
	if panelIDLen > panelIDMaxLen || 1+panelIDLen+8+8+nonceSize > len(data) {
		return nil, ErrInvalidToken
	}

	offset := 1
	panelID := string(data[offset : offset+panelIDLen])
	offset += panelIDLen

	issuedAt := int64(getUint64(data[offset:]))
	offset += 8
	expiresAt := int64(getUint64(data[offset:]))
	offset += 8

	nonce := make([]byte, nonceSize)
	copy(nonce, data[offset:offset+nonceSize])

	return &Token{
		PanelID:   panelID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
		Nonce:     nonce,
	}, nil
}

func putUint64(buf []byte, v uint64) {
	buf[0] = byte(v >> 56)
	buf[1] = byte(v >> 48)
	buf[2] = byte(v >> 40)
	buf[3] = byte(v >> 32)
	buf[4] = byte(v >> 24)
	buf[5] = byte(v >> 16)
	buf[6] = byte(v >> 8)
	buf[7] = byte(v)
}

func getUint64(buf []byte) uint64 {
	return uint64(buf[0])<<56 | uint64(buf[1])<<48 | uint64(buf[2])<<40 | uint64(buf[3])<<32 |
		uint64(buf[4])<<24 | uint64(buf[5])<<16 | uint64(buf[6])<<8 | uint64(buf[7])
}

func ConstantTimeCompare(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}