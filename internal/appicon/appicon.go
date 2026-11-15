// Package appicon draws Shuttle Server's app icon (the page's logo: a purple square with an up
// and a down arrow) and turns it into a macOS .icns file, so Shuttle's
// helper apps show their own icon and name in System Settings and Finder.
// Pure Go for the drawing; macOS's own iconutil makes the .icns.
package appicon

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The page's own logo (the favicon in web/index.html), in its 64-unit space:
// a purple rounded square with an up arrow and a down arrow in round white
// strokes 5 units wide.
var purple = color.NRGBA{R: 0x7a, G: 0x3c, B: 0xc8, A: 0xff}

type seg struct{ x1, y1, x2, y2 float64 }

var arrows = []seg{
	{22, 46, 22, 18}, {22, 18, 14, 26}, {22, 18, 30, 26}, // up
	{42, 18, 42, 46}, {42, 46, 34, 38}, {42, 46, 50, 38}, // down
}

const (
	corner = 14.0
	half   = 2.5 // half the stroke width
)

func distToSeg(px, py float64, s seg) float64 {
	dx, dy := s.x2-s.x1, s.y2-s.y1
	t := ((px-s.x1)*dx + (py-s.y1)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	ex, ey := px-(s.x1+t*dx), py-(s.y1+t*dy)
	return math.Sqrt(ex*ex + ey*ey)
}

// inShape reports whether the point (x, y), in 0..1, is on the square and on an arrow.
func inShape(x, y float64) (square, arrow bool) {
	px, py := x*64, y*64
	cx, cy := px, py
	if px < corner {
		cx = corner
	} else if px > 64-corner {
		cx = 64 - corner
	}
	if py < corner {
		cy = corner
	} else if py > 64-corner {
		cy = 64 - corner
	}
	dx, dy := px-cx, py-cy
	square = dx*dx+dy*dy <= corner*corner
	for _, a := range arrows {
		if distToSeg(px, py, a) <= half {
			return square, true
		}
	}
	return square, false
}

// Render draws the icon at size×size pixels, smoothed by 4×4 sampling.
func Render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const n = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sq, ar int
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					s, a := inShape((float64(px)+(float64(sx)+0.5)/n)/float64(size), (float64(py)+(float64(sy)+0.5)/n)/float64(size))
					if s {
						sq++
						if a {
							ar++
						}
					}
				}
			}
			if sq == 0 {
				continue
			}
			w := float64(ar) / float64(sq) // white share of the covered samples
			c := color.NRGBA{
				R: uint8(float64(purple.R)*(1-w) + 255*w),
				G: uint8(float64(purple.G)*(1-w) + 255*w),
				B: uint8(float64(purple.B)*(1-w) + 255*w),
				A: uint8(255 * sq / (n * n)),
			}
			img.SetNRGBA(px, py, c)
		}
	}
	return img
}

// PNG is the icon as a PNG file.
func PNG(size int) []byte {
	var b bytes.Buffer
	png.Encode(&b, Render(size))
	return b.Bytes()
}

var iconset = []struct {
	name string
	px   int
}{
	{"icon_16x16.png", 16}, {"icon_16x16@2x.png", 32}, {"icon_32x32.png", 32}, {"icon_32x32@2x.png", 64},
	{"icon_128x128.png", 128}, {"icon_128x128@2x.png", 256}, {"icon_256x256.png", 256}, {"icon_256x256@2x.png", 512},
	{"icon_512x512.png", 512}, {"icon_512x512@2x.png", 1024},
}

// WriteIconset writes the ten PNG sizes macOS expects into dir.
func WriteIconset(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cache := map[int][]byte{}
	for _, f := range iconset {
		b, ok := cache[f.px]
		if !ok {
			b = PNG(f.px)
			cache[f.px] = b
		}
		if err := os.WriteFile(filepath.Join(dir, f.name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WriteICNS makes the .icns file at path with macOS's iconutil.
func WriteICNS(ctx context.Context, path string) error {
	tmp, err := os.MkdirTemp("", "shuttle-icon-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // our own scratch folder
	set := filepath.Join(tmp, "Shuttle.iconset")
	if err := WriteIconset(set); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/usr/bin/iconutil", "-c", "icns", "-o", path, set).CombinedOutput(); err != nil {
		return fmt.Errorf("iconutil: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
