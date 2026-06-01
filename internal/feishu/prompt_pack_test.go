package feishu

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
	"strings"
	"testing"
)

func TestPromptPackRendersEmbeddedModules(t *testing.T) {
	ConfigurePromptPack(PromptPackOptions{AppVersion: "1.6.7"})
	t.Cleanup(func() { ConfigurePromptPack(PromptPackOptions{}) })
	prompt := activePromptRulesText()
	for _, want := range []string{
		"官方 `lark-cli`",
		"drive permission.public patch",
		"agent_reading.has_more",
		"Skill",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("embedded prompt missing %q", want)
		}
	}
}

func TestPromptPackRejectsUnsignedRemotePack(t *testing.T) {
	_, pub, server := signedPromptPackServer(t, func(m *promptPackManifest) {
		m.Signature = base64.StdEncoding.EncodeToString([]byte("bad"))
	})
	defer server.Close()

	ConfigurePromptPack(PromptPackOptions{
		ManifestURL:     server.URL + "/manifest.json",
		PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		AppVersion:      "1.6.7",
	})
	t.Cleanup(func() { ConfigurePromptPack(PromptPackOptions{}) })
	_, err := CheckPromptPackUpdate(context.Background())
	if err == nil {
		t.Fatal("expected bad signature to be rejected")
	}
	if !strings.Contains(GetPromptPackStatus().LastError, "签名") {
		t.Fatalf("expected signature error, got %+v", GetPromptPackStatus())
	}
}

func TestPromptPackAppliesSignedRemotePack(t *testing.T) {
	_, pub, server := signedPromptPackServer(t, nil)
	defer server.Close()

	ConfigurePromptPack(PromptPackOptions{
		ManifestURL:     server.URL + "/manifest.json",
		PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		AppVersion:      "1.6.7",
	})
	t.Cleanup(func() { ConfigurePromptPack(PromptPackOptions{}) })
	status, err := CheckPromptPackUpdate(context.Background())
	if err != nil {
		t.Fatalf("check prompt pack: %v", err)
	}
	if status.Source != "remote" || status.Version != "2026.05.28.1" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if !strings.Contains(activePromptRulesText(), "REMOTE_BASE_RULE") {
		t.Fatal("remote prompt rules should be active")
	}
}

func TestPromptPackFillsNewModulesFromEmbedded(t *testing.T) {
	_, pub, server := signedPromptPackServer(t, func(m *promptPackManifest) {}, func(pack *promptPackFile) {
		delete(pack.Modules, "schedule_templates")
		nextOrder := make([]string, 0, len(pack.ModuleOrder))
		for _, name := range pack.ModuleOrder {
			if name != "schedule_templates" {
				nextOrder = append(nextOrder, name)
			}
		}
		pack.ModuleOrder = nextOrder
	})
	defer server.Close()

	ConfigurePromptPack(PromptPackOptions{
		ManifestURL:     server.URL + "/manifest.json",
		PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
		AppVersion:      "1.6.9",
	})
	t.Cleanup(func() { ConfigurePromptPack(PromptPackOptions{}) })
	status, err := CheckPromptPackUpdate(context.Background())
	if err != nil {
		t.Fatalf("old prompt pack should use embedded fallback: %v", err)
	}
	if status.Source != "remote" {
		t.Fatalf("unexpected status: %+v", status)
	}
	prompt := activePromptRulesText()
	if !strings.Contains(prompt, "REMOTE_BASE_RULE") || !strings.Contains(prompt, "AI Radar 日报模板") {
		t.Fatalf("prompt should combine remote pack and embedded new module:\n%s", prompt)
	}
}

func signedPromptPackServer(t *testing.T, mutate func(*promptPackManifest), packMutators ...func(*promptPackFile)) (ed25519.PrivateKey, ed25519.PublicKey, *httptest.Server) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	modules := map[string]string{}
	for _, name := range promptRuleOrder {
		modules[name] = strings.ToUpper(name) + "_REMOTE_BASE_RULE"
	}
	pack := promptPackFile{
		SchemaVersion: promptPackSchemaVersion,
		Channel:       promptPackChannel,
		Version:       "2026.05.28.1",
		MinAppVersion: "1.6.7",
		Modules:       modules,
		ModuleOrder:   promptRuleOrder,
	}
	for _, mutatePack := range packMutators {
		mutatePack(&pack)
	}
	packBytes, err := json.Marshal(pack)
	if err != nil {
		t.Fatalf("marshal pack: %v", err)
	}
	sum := sha256.Sum256(packBytes)
	sha := hex.EncodeToString(sum[:])
	manifest := promptPackManifest{
		SchemaVersion: promptPackSchemaVersion,
		Channel:       promptPackChannel,
		Version:       pack.Version,
		MinAppVersion: pack.MinAppVersion,
		SHA256:        sha,
		Signature:     base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sha))),
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			next := manifest
			next.URL = server.URL + "/prompt-pack.json"
			if mutate != nil {
				mutate(&next)
			}
			_ = json.NewEncoder(w).Encode(next)
		case "/prompt-pack.json":
			_, _ = w.Write(packBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	old := globalPromptPack.httpClient
	globalPromptPack.httpClient = server.Client()
	t.Cleanup(func() {
		globalPromptPack.httpClient = old
	})
	return priv, pub, server
}
