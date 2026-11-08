package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aviary/agent/internal/auth"
)

func main() {
	output := flag.String("output", "keys", "Output directory for keys")
	generateToken := flag.Bool("token", false, "Generate token instead of keys")
	panelID := flag.String("panel-id", "panel-1", "Panel ID for token")
	privateKeyFile := flag.String("private-key", "", "Private key file for token generation")
	expiry := flag.Duration("expiry", 1*time.Hour, "Token expiry duration")
	flag.Parse()

	if *generateToken {
		if *privateKeyFile == "" {
			fmt.Fprintln(os.Stderr, "private-key flag required for token generation")
			os.Exit(1)
		}
		generateTokenCmd(*panelID, *privateKeyFile, *expiry)
		return
	}

	generateKeys(*output)
}

func generateKeys(outputDir string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate keys: %v\n", err)
		os.Exit(1)
	}

	pubB64 := base64.StdEncoding.EncodeToString(pub)
	privB64 := base64.StdEncoding.EncodeToString(priv)

	if err := os.MkdirAll(outputDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output dir: %v\n", err)
		os.Exit(1)
	}

	pubPath := fmt.Sprintf("%s/panel_public.key", outputDir)
	privPath := fmt.Sprintf("%s/panel_private.key", outputDir)

	if err := os.WriteFile(pubPath, []byte(pubB64), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write public key: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(privPath, []byte(privB64), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write private key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Keys generated in %s/\n", outputDir)
	fmt.Printf("Public key (for agent config):\n%s\n", pubB64)
	fmt.Printf("Private key (for panel token generation):\n%s\n", privB64)
}

func generateTokenCmd(panelID, privateKeyFile string, expiry time.Duration) {
	keyData, err := os.ReadFile(privateKeyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read private key: %v\n", err)
		os.Exit(1)
	}

	privBytes, err := base64.StdEncoding.DecodeString(string(keyData))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode private key: %v\n", err)
		os.Exit(1)
	}

	if len(privBytes) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "Invalid private key size")
		os.Exit(1)
	}

	privKey := ed25519.PrivateKey(privBytes)
	token, err := auth.GenerateToken(panelID, privKey, expiry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(token)
}