// Package appicon draws Shuttle Server's app icon (a purple square with an up
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
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var purple = color.NRGBA{R: 0x7c, G: 0x3a, B: 0xed, A: 0xff}

// inShape reports whether the point (x, y), in 0..1, is on the icon.
func inShape(x, y float64) (square, arrow bool) {
	const m, r = 0.04, 0.22 // margin and corner radius
	cx, cy := x, y
	switch {
	case x < m+r:
		cx = m + r
	case x > 1-m-r:
		cx = 1 - m - r
	}
	switch {
	case y < m+r:
		cy = m + r
	case y > 1-m-r:
		cy = 1 - m - r
	}
	dx, dy := x-cx, y-cy
	square = x >= m && x <= 1-m && y >= m && y <= 1-m && dx*dx+dy*dy <= r*r
	tri := func(ax, ay, by, hw float64) bool { // apex (ax, ay), base at by, half width hw
		t := (y - ay) / (by - ay)
		return t >= 0 && t <= 1 && abs(x-ax) <= hw*t
	}
	up := (x >= 0.33 && x <= 0.39 && y >= 0.38 && y <= 0.76) || tri(0.36, 0.22, 0.42, 0.12)
	down := (x >= 0.61 && x <= 0.67 && y >= 0.24 && y <= 0.62) || tri(0.64, 0.78, 0.58, 0.12)
	return square, up || down
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
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
