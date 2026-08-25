package remote

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func makePNGDataURL(w, h int) string {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, im)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDewatermarkDataURLKeepsPNGSameSize(t *testing.T) {
	in := makePNGDataURL(200, 150)
	out := dewatermarkDataURL(in)
	const pp = "data:image/png;base64,"
	if !strings.HasPrefix(out, pp) {
		t.Fatalf("expected png data url, got %.40s", out)
	}
	if out == in {
		t.Fatal("expected the image to be transformed, not passed through")
	}
	raw, err := base64.StdEncoding.DecodeString(out[len(pp):])
	if err != nil {
		t.Fatalf("b64: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 150 {
		t.Fatalf("dims changed: %v", img.Bounds())
	}
}

func TestDewmCropForSmallImagesStillDesyncs(t *testing.T) {
	if got := dewmCropFor(1024, 1024); got != dewmCropPx {
		t.Fatalf("dewmCropFor(1024,1024)=%d, want %d", got, dewmCropPx)
	}
	// Small images must still get a non-zero crop (else the desync is a no-op).
	for _, sz := range []int{80, 64, 16, 8} {
		if got := dewmCropFor(sz, sz); got < 1 {
			t.Fatalf("dewmCropFor(%d,%d)=%d, want >=1 so the geometric desync still runs", sz, sz, got)
		}
	}
}

func TestDewatermarkSmallImageIsTransformed(t *testing.T) {
	in := makePNGDataURL(64, 64)
	out := dewatermarkDataURL(in)
	if out == in {
		t.Fatal("small image should still be desynced, not passed through")
	}
	const pp = "data:image/png;base64,"
	raw, _ := base64.StdEncoding.DecodeString(out[len(pp):])
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("dims changed: %v", img.Bounds())
	}
}

func TestDewatermarkPassthroughNonPNG(t *testing.T) {
	if got := dewatermarkDataURL("https://example.com/y.png"); got != "https://example.com/y.png" {
		t.Fatal("plain URL should pass through unchanged")
	}
	if got := dewatermarkDataURL("data:image/jpeg;base64,AAAA"); got != "data:image/jpeg;base64,AAAA" {
		t.Fatal("non-PNG data URL should pass through unchanged")
	}
}
