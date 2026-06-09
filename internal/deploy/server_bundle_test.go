package deploy

import (
	"archive/zip"
	"path/filepath"
	"testing"

	"lingma-ipc-proxy/internal/remote"
)

func TestWriteServerBundle(t *testing.T) {
	dir := t.TempDir()
	sourceCredPath := filepath.Join(dir, "source-credentials.json")
	if err := remote.SaveCredentialFile(remote.Credential{
		CosyKey:         "cosy",
		EncryptUserInfo: "encrypted",
		UserID:          "user-123456",
		MachineID:       "machine-1234567890",
		Source:          "test",
		TokenExpireTime: 1777520000000,
	}, sourceCredPath); err != nil {
		t.Fatalf("SaveCredentialFile() error = %v", err)
	}

	outputPath := filepath.Join(dir, "bundle.zip")
	result, err := WriteServerBundle(ServerBundleOptions{
		AuthFile:   sourceCredPath,
		OutputPath: outputPath,
		Port:       18095,
		Model:      "kmodel",
	})
	if err != nil {
		t.Fatalf("WriteServerBundle() error = %v", err)
	}
	if result.Path != outputPath {
		t.Fatalf("bundle path = %q, want %q", result.Path, outputPath)
	}
	if result.CredentialSrc != "test" {
		t.Fatalf("credential source = %q, want test", result.CredentialSrc)
	}

	reader, err := zip.OpenReader(outputPath)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer reader.Close()
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	for _, name := range []string{"credentials.json", "lingma-proxy.json", "docker-compose.yml", "README.txt"} {
		if !names[name] {
			t.Fatalf("bundle missing %s", name)
		}
	}
}
