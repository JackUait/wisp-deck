package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func pngEncodeBench(buf *bytes.Buffer, img image.Image, b *testing.B) error {
	b.Helper()
	return png.Encode(buf, img)
}

// photoJPEG builds a 1920×1080 JPEG with per-pixel variation (a photo-like
// payload — a solid color would compress unrealistically well).
func photoJPEG(b *testing.B) []byte {
	b.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			img.SetRGBA(x, y, color.RGBA{
				uint8(x*7 + y*3), uint8(x*2 ^ y*5), uint8(x*13 + y*11), 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		b.Fatal(err)
	}
	return buf.Bytes()
}

func BenchmarkImageOpen_decode(b *testing.B) {
	data := photoJPEG(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewImageView("x.jpg", data, "added")
	}
}

func BenchmarkImageOpen_halfblocks(b *testing.B) {
	m := NewImageView("x.jpg", photoJPEG(b), "added")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.renderImageBody(200, 48)
	}
}

func BenchmarkImageOpen_payloadJPEG(b *testing.B) {
	m := NewImageView("x.jpg", photoJPEG(b), "added")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kittyPayload(m.img, m.imgSrc)
	}
}

func BenchmarkImageOpen_payloadPNG(b *testing.B) {
	var buf bytes.Buffer
	img, _, _ := image.Decode(bytes.NewReader(photoJPEG(b)))
	_ = pngEncodeBench(&buf, img, b)
	m := NewImageView("x.png", buf.Bytes(), "added")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kittyPayload(m.img, m.imgSrc)
	}
}

func BenchmarkImageOpen_transmitBytes(b *testing.B) {
	m := NewImageView("x.jpg", photoJPEG(b), "added")
	payload := kittyPayload(m.img, m.imgSrc)
	seq := kittyTransmitDisplay(payload, 1, 171, 48, false)
	b.ReportMetric(float64(len(payload)), "png_bytes")
	b.ReportMetric(float64(len(seq)), "wire_bytes")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kittyTransmitDisplay(payload, 1, 171, 48, false)
	}
}
