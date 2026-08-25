package remote

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"strings"
)

// Optional blind-watermark payload disruption for generated images, enabled via
// LINGMA_IMAGE_DEWATERMARK. A single lossy re-encode leaves the mark intact; a
// non-invertible geometric desync (crop a margin, then resize back to the original
// size) destroys it because the decoder reads off a shifted grid, and a lossy JPEG
// pass adds damage. The op must NOT be undone (rotate-back re-syncs it); the result
// is delivered as a same-size PNG. Corrupts the payload, not detectability.
const (
	dewmCropPx      = 32
	dewmJPEGQuality = 30
)

func dewatermarkEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LINGMA_IMAGE_DEWATERMARK"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// dewatermarkDataURL applies the desync+JPEG pass to a PNG data URL, returning a
// fresh PNG. Non-PNG input or any failure is returned unchanged (delivery must not fail).
func dewatermarkDataURL(dataURL string) string {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return dataURL
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[len(prefix):])
	if err != nil {
		return dataURL
	}
	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return dataURL
	}
	out, err := desyncRecompress(src)
	if err != nil {
		return dataURL
	}
	return prefix + base64.StdEncoding.EncodeToString(out)
}

// desyncRecompress crops a margin, JPEG-recompresses, then resizes back to the
// original size (the non-invertible desync) and PNG-encodes.
func desyncRecompress(src image.Image) ([]byte, error) {
	b := src.Bounds()
	W, H := b.Dx(), b.Dy()
	rgba := image.NewRGBA(image.Rect(0, 0, W, H)) // flatten for fast/consistent access
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	crop := dewmCropFor(W, H)

	// 1) lossy JPEG at the cropped resolution (destructive step)
	sub := rgba.SubImage(image.Rect(crop, crop, W-crop, H-crop))
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, sub, &jpeg.Options{Quality: dewmJPEGQuality}); err != nil {
		return nil, err
	}
	jimg, err := jpeg.Decode(&jb)
	if err != nil {
		return nil, err
	}
	cw, ch := jimg.Bounds().Dx(), jimg.Bounds().Dy()
	small := image.NewRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(small, small.Bounds(), jimg, jimg.Bounds().Min, draw.Src)

	// 2) bilinear-resize the cropped image back up to the original size
	out := bilinearResizeRGBA(small, W, H)

	// 3) deliver as PNG (no JPEG file signature / 8x8 grid in the result)
	var pb bytes.Buffer
	if err := png.Encode(&pb, out); err != nil {
		return nil, err
	}
	return pb.Bytes(), nil
}

// dewmCropFor picks the crop margin: the fixed dewmCropPx for normal images, or a
// proportional crop for small ones so the desync still happens (crop 0 = no-op).
func dewmCropFor(W, H int) int {
	crop := dewmCropPx
	if m := min(W, H); 2*crop+16 >= m {
		crop = m / 8
		if crop < 1 && m >= 3 {
			crop = 1
		}
	}
	return crop
}

// bilinearResizeRGBA scales src to dstW x dstH using bilinear interpolation.
func bilinearResizeRGBA(src *image.RGBA, dstW, dstH int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	at := func(x, y int) (r, g, bl uint8) {
		i := src.PixOffset(x, y)
		return src.Pix[i], src.Pix[i+1], src.Pix[i+2]
	}
	for dy := 0; dy < dstH; dy++ {
		fy := (float64(dy)+0.5)*float64(sh)/float64(dstH) - 0.5
		y0 := int(math.Floor(fy))
		wy := fy - float64(y0)
		y0c := clampInt(y0, 0, sh-1)
		y1c := clampInt(y0+1, 0, sh-1)
		for dx := 0; dx < dstW; dx++ {
			fx := (float64(dx)+0.5)*float64(sw)/float64(dstW) - 0.5
			x0 := int(math.Floor(fx))
			wx := fx - float64(x0)
			x0c := clampInt(x0, 0, sw-1)
			x1c := clampInt(x0+1, 0, sw-1)
			r00, g00, b00 := at(x0c, y0c)
			r10, g10, b10 := at(x1c, y0c)
			r01, g01, b01 := at(x0c, y1c)
			r11, g11, b11 := at(x1c, y1c)
			o := out.PixOffset(dx, dy)
			out.Pix[o] = bilerp(r00, r10, r01, r11, wx, wy)
			out.Pix[o+1] = bilerp(g00, g10, g01, g11, wx, wy)
			out.Pix[o+2] = bilerp(b00, b10, b01, b11, wx, wy)
			out.Pix[o+3] = 255
		}
	}
	return out
}

func bilerp(c00, c10, c01, c11 uint8, wx, wy float64) uint8 {
	top := float64(c00)*(1-wx) + float64(c10)*wx
	bot := float64(c01)*(1-wx) + float64(c11)*wx
	v := top*(1-wy) + bot*wy
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return uint8(v + 0.5)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
