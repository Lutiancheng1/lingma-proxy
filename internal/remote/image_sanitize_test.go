package remote

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

func pngChunk(typ string, data []byte) []byte {
	b := make([]byte, 0, 12+len(data))
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	b = append(b, l[:]...)
	b = append(b, []byte(typ)...)
	b = append(b, data...)
	b = append(b, 0, 0, 0, 0) // CRC is not verified by the stripper
	return b
}

func buildTestPNG() []byte {
	png := append([]byte{}, pngSignature...)
	png = append(png, pngChunk("IHDR", make([]byte, 13))...)
	png = append(png, pngChunk("tEXt", []byte("AIGC\x00{\"ContentProducer\":\"001191330106MA2CFLDG4R10001\"}"))...)
	png = append(png, pngChunk("IDAT", []byte("pixel-data"))...)
	png = append(png, pngChunk("IEND", nil)...)
	return png
}

func TestStripPNGMetadata(t *testing.T) {
	cleaned, ok := stripPNGMetadata(buildTestPNG())
	if !ok {
		t.Fatal("expected a well-formed PNG")
	}
	if bytes.Contains(cleaned, []byte("ContentProducer")) || bytes.Contains(cleaned, []byte("tEXt")) {
		t.Fatalf("AIGC tEXt metadata was not stripped: %q", cleaned)
	}
	for _, keep := range []string{"IHDR", "IDAT", "IEND"} {
		if !bytes.Contains(cleaned, []byte(keep)) {
			t.Fatalf("critical chunk %s was dropped", keep)
		}
	}
	if _, ok := stripPNGMetadata([]byte("not a png")); ok {
		t.Fatal("non-PNG input should report false")
	}
}

func TestStripPNGMetadataRejectsOversizedChunkLength(t *testing.T) {
	// A chunk length far exceeding the buffer must be rejected by the bounds check.
	buf := append([]byte(nil), pngSignature...)
	buf = append(buf, 0x7F, 0xFF, 0xFF, 0xFF) // declared length ~2^31
	buf = append(buf, []byte("tEXt")...)
	buf = append(buf, 0, 0, 0, 0) // CRC placeholder so the loop enters (needs p+12 bytes)
	if _, ok := stripPNGMetadata(buf); ok {
		t.Fatal("expected stripPNGMetadata to reject an oversized chunk length")
	}
}

func TestStripPNGMetadataRejectsMissingIEND(t *testing.T) {
	// A PNG with no IEND is incomplete; the stripper must report false.
	png := append([]byte(nil), pngSignature...)
	png = append(png, pngChunk("IHDR", make([]byte, 13))...)
	png = append(png, pngChunk("IDAT", []byte("pixel-data"))...)
	// deliberately no IEND chunk
	if _, ok := stripPNGMetadata(png); ok {
		t.Fatal("expected stripPNGMetadata to reject a PNG with no IEND chunk")
	}
}

func TestSanitizeImageDataURL(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buildTestPNG())
	got := sanitizeImageDataURL(dataURL)
	png, err := base64.StdEncoding.DecodeString(got[len("data:image/png;base64,"):])
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if bytes.Contains(png, []byte("ContentProducer")) {
		t.Fatal("data URL sanitize left AIGC metadata behind")
	}
	// Non-PNG / non-data URLs pass through unchanged.
	if sanitizeImageDataURL("https://example.com/a.png") != "https://example.com/a.png" {
		t.Fatal("non-data URL should pass through unchanged")
	}
}
