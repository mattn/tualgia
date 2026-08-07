package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	imageFetchTimeout = 10 * time.Second
	imageMaxBytes     = 20 * 1024 * 1024
	imageMaxWidth     = 800
	imageMaxHeight    = 480
)

func isImageURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	switch strings.ToLower(path.Ext(pu.Path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// fetchImageSixel downloads an image, scales it down to fit maxW x maxH and
// returns it encoded as sixel.
func fetchImageSixel(u string, maxW, maxH int) ([]byte, error) {
	client := &http.Client{Timeout: imageFetchTimeout}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", name+"/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes))
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("cannot decode %s: %w", u, err)
	}

	img = fitImage(img, maxW, maxH)

	var buf bytes.Buffer
	if err := sixel.NewEncoder(&buf).Encode(img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fitImage scales img down to fit within maxW x maxH keeping the aspect
// ratio. Images that already fit are returned as-is.
func fitImage(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW && h <= maxH {
		return img
	}
	scale := float64(maxW) / float64(w)
	if s := float64(maxH) / float64(h); s < scale {
		scale = s
	}
	nw := max(int(float64(w)*scale), 1)
	nh := max(int(float64(h)*scale), 1)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}
