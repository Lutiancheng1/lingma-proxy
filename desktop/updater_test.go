package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.6.8", "1.6.7", 1},
		{"1.6.7", "1.6.7", 0},
		{"v1.6.6", "1.6.7", -1},
		{"1.10.0", "1.9.9", 1},
	}
	for _, tc := range cases {
		got := compareVersion(tc.a, tc.b)
		if (got > 0) != (tc.want > 0) || (got < 0) != (tc.want < 0) {
			t.Fatalf("compareVersion(%q,%q)=%d want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestVerifySignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("payload"))
	shaHex := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, []byte(shaHex))
	if err := verifySignature(shaHex, base64.StdEncoding.EncodeToString(sig), base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifySignature("bad", base64.StdEncoding.EncodeToString(sig), base64.StdEncoding.EncodeToString(pub)); err == nil {
		t.Fatal("expected invalid signature to be rejected")
	}
}

func TestFetchUpdateManifest(t *testing.T) {
	manifest := updateManifest{
		SchemaVersion: updateSchemaVersion,
		Channel:       updateChannel,
		Version:       "1.6.8",
		Assets: map[string]OnlineUpdateAsset{
			updatePlatformKey(): {URL: "https://example.com/update.bin", SHA256: "abc", Signature: "sig", Kind: "dmg"},
		},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()
	oldClient := http.DefaultClient
	http.DefaultClient = server.Client()
	t.Cleanup(func() { http.DefaultClient = oldClient })

	got, err := fetchUpdateManifest(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	if got.Version != manifest.Version {
		t.Fatalf("unexpected version %q", got.Version)
	}
}
