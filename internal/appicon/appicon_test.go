package appicon

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestIconIsAPurpleSquareWithWhiteArrows(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG(256)))
	if err != nil || img.Bounds().Dx() != 256 {
		t.Fatal(err)
	}
	r, g, b, a := img.At(128, 40).RGBA() // inside the square, between the arrows' tops: purple
	if a>>8 != 255 || r>>8 != 0x7c || g>>8 != 0x3a || b>>8 != 0xed {
		t.Fatalf("background not purple: %x %x %x %x", r>>8, g>>8, b>>8, a>>8)
	}
	if r, g, b, _ := img.At(92, 150).RGBA(); r>>8 != 255 || g>>8 != 255 || b>>8 != 255 { // up arrow's shaft
		t.Fatal("arrow not white")
	}
	if _, _, _, a := img.At(1, 1).RGBA(); a != 0 { // outside the rounded corner
		t.Fatal("corner should be see-through")
	}
}

func TestIconsetHasEverySize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x.iconset")
	if err := WriteIconset(dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range iconset {
		fi, err := os.Stat(filepath.Join(dir, f.name))
		if err != nil || fi.Size() == 0 {
			t.Fatalf("%s: %v", f.name, err)
		}
	}
}
