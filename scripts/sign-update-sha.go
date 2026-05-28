//go:build ignore

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/sign-update-sha.go <sha256-hex>")
		os.Exit(2)
	}
	raw := strings.TrimSpace(os.Getenv("UPDATE_SIGNING_PRIVATE_KEY"))
	if raw == "" {
		fmt.Fprintln(os.Stderr, "UPDATE_SIGNING_PRIVATE_KEY is required")
		os.Exit(2)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid UPDATE_SIGNING_PRIVATE_KEY:", err)
		os.Exit(2)
	}
	switch len(key) {
	case ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(key)
	case ed25519.PrivateKeySize:
	default:
		fmt.Fprintln(os.Stderr, "UPDATE_SIGNING_PRIVATE_KEY must be raw ed25519 seed(32) or private key(64), base64 encoded")
		os.Exit(2)
	}
	sig := ed25519.Sign(ed25519.PrivateKey(key), []byte(strings.TrimSpace(os.Args[1])))
	fmt.Print(base64.StdEncoding.EncodeToString(sig))
}
