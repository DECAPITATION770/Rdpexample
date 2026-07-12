package capture

import (
	"image"
	"image/color"
	"testing"
)

func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestFrameHash_IdenticalImagesMatch(t *testing.T) {
	a := solidImage(64, 48, color.RGBA{10, 20, 30, 255})
	b := solidImage(64, 48, color.RGBA{10, 20, 30, 255})
	if frameHash(a) != frameHash(b) {
		t.Fatal("identical images should hash equal")
	}
}

func TestFrameHash_DifferentImagesDiffer(t *testing.T) {
	a := solidImage(64, 48, color.RGBA{10, 20, 30, 255})
	b := solidImage(64, 48, color.RGBA{10, 20, 30, 255})
	b.SetRGBA(5, 5, color.RGBA{200, 0, 0, 255}) // one pixel differs
	if frameHash(a) == frameHash(b) {
		t.Fatal("a one-pixel change should flip the hash")
	}
}

func TestDownscale_NoOpWhenWithinMaxWidth(t *testing.T) {
	img := solidImage(100, 50, color.RGBA{1, 2, 3, 255})
	// maxWidth 0 (disabled) and maxWidth >= width should both return the
	// exact same object — no copy, no scaling.
	if got := downscale(img, 0); got != img {
		t.Error("maxWidth 0 should return the original image unchanged")
	}
	if got := downscale(img, 100); got != img {
		t.Error("maxWidth == width should return the original image unchanged")
	}
	if got := downscale(img, 200); got != img {
		t.Error("maxWidth > width should return the original image unchanged")
	}
}

func TestDownscale_ShrinksAndKeepsAspectRatio(t *testing.T) {
	img := solidImage(1920, 1080, color.RGBA{5, 5, 5, 255})
	got := downscale(img, 1280)
	b := got.Bounds()
	if b.Dx() != 1280 {
		t.Errorf("width = %d, want 1280", b.Dx())
	}
	if b.Dy() != 720 { // 1080 * 1280 / 1920
		t.Errorf("height = %d, want 720 (aspect ratio preserved)", b.Dy())
	}
}

func TestEncodeJPEG_ReturnsPostDownscaleDimensions(t *testing.T) {
	img := solidImage(1920, 1080, color.RGBA{100, 150, 200, 255})
	data, w, h, err := encodeJPEG(img, 75, 960)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JPEG output")
	}
	if w != 960 || h != 540 {
		t.Errorf("dimensions = %dx%d, want 960x540 (the encoded/downscaled size, not native)", w, h)
	}
}

func TestEncodeJPEG_NativeWhenNoMaxWidth(t *testing.T) {
	img := solidImage(800, 600, color.RGBA{0, 0, 0, 255})
	_, w, h, err := encodeJPEG(img, 75, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w != 800 || h != 600 {
		t.Errorf("dimensions = %dx%d, want native 800x600", w, h)
	}
}
