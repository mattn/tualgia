package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"testing"

	"net/http"
)

func TestIsImageURL(t *testing.T) {
	tests := map[string]bool{
		"https://example.com/a.png":         true,
		"https://example.com/a.JPG":         true,
		"https://example.com/a.webp?w=100":  true,
		"https://example.com/a.gif":         true,
		"https://example.com/index.html":    false,
		"https://example.com/":              false,
		"https://example.com/movie.mp4":     false,
		"https://example.com/a.png/related": false,
	}
	for u, want := range tests {
		if got := isImageURL(u); got != want {
			t.Errorf("isImageURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestFetchImageSixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 300))
	for x := 0; x < 1200; x++ {
		for y := 0; y < 300; y++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(buf.Bytes())
	}))
	defer ts.Close()

	b, err := fetchImageSixel(ts.URL+"/test.png", 800, 480)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || !bytes.HasPrefix(b, []byte("\x1bP")) {
		t.Fatalf("not a sixel sequence: %q...", b[:min(len(b), 10)])
	}
}

func TestFitImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 400))
	got := fitImage(img, 800, 480).Bounds()
	if got.Dx() != 800 || got.Dy() != 200 {
		t.Errorf("fitImage: got %dx%d, want 800x200", got.Dx(), got.Dy())
	}
	small := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if fitImage(small, 800, 480) != small {
		t.Error("small image should be returned as-is")
	}
}
