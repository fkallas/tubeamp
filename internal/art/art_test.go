package art

import (
	"image"
	"image/color"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain forces a deterministic colour profile so tests do not depend on the
// calling terminal's capabilities.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// ansiRE matches ANSI SGR escape sequences (e.g. \x1b[38;2;255;0;0m).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI escape sequences from s, leaving plain text.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// solidImage creates a cols×rows *image.RGBA filled with a single RGBA value.
func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// TestRenderDimensions verifies that Render produces exactly rows lines and
// exactly cols UPPER HALF BLOCK cells per line.
func TestRenderDimensions(t *testing.T) {
	const cols, rows = 20, 10
	img := solidImage(40, 30, color.RGBA{200, 100, 50, 255})

	out := Render(img, cols, rows, Options{})
	lines := strings.Split(out, "\n")
	if len(lines) != rows {
		t.Fatalf("want %d lines, got %d", rows, len(lines))
	}
	for i, line := range lines {
		plain := stripANSI(line)
		runes := []rune(plain)
		if len(runes) != cols {
			t.Errorf("line %d: want %d cells, got %d (plain=%q)", i, cols, len(runes), plain)
		}
		for j, r := range runes {
			if r != '▀' {
				t.Errorf("line %d, cell %d: want ▀, got %q", i, j, r)
			}
		}
	}
}

// TestRenderDimensionsSmall tests an image smaller than the target to exercise
// the up-scaling code path.
func TestRenderDimensionsSmall(t *testing.T) {
	const cols, rows = 8, 4
	img := solidImage(4, 4, color.RGBA{0, 200, 0, 255})

	out := Render(img, cols, rows, Options{})
	lines := strings.Split(out, "\n")
	if len(lines) != rows {
		t.Fatalf("want %d lines, got %d", rows, len(lines))
	}
	for _, line := range lines {
		if n := len([]rune(stripANSI(line))); n != cols {
			t.Errorf("want %d cells, got %d", cols, n)
		}
	}
}

// TestFixedPalettePixels verifies that every pixel of a palette-rendered image
// is one of the palette colors, inspected via RenderToImage (no ANSI parsing).
func TestFixedPalettePixels(t *testing.T) {
	palette := []color.Color{
		color.RGBA{R: 255, G: 0, B: 0, A: 255},
		color.RGBA{R: 0, G: 255, B: 0, A: 255},
		color.RGBA{R: 0, G: 0, B: 255, A: 255},
	}
	// Expected RGBA values for comparison.
	palRGBA := []color.RGBA{
		{R: 255, G: 0, B: 0, A: 255},
		{R: 0, G: 255, B: 0, A: 255},
		{R: 0, G: 0, B: 255, A: 255},
	}

	// Gradient image with many colors.
	img := image.NewRGBA(image.Rect(0, 0, 30, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 30; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 8),
				G: uint8(y * 12),
				B: uint8((x + y) * 4),
				A: 255,
			})
		}
	}

	processed := RenderToImage(img, 10, 5, Options{Palette: palette})
	bd := processed.Bounds()
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			p := processed.RGBAAt(x, y)
			found := false
			for _, pc := range palRGBA {
				if p.R == pc.R && p.G == pc.G && p.B == pc.B {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("pixel (%d,%d) = %v not in palette", x, y, p)
			}
		}
	}
}

// TestMedianCutDistinctColors verifies that median-cut quantisation produces at
// most N distinct colors in the output image.
func TestMedianCutDistinctColors(t *testing.T) {
	const N = 4

	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 6),
				G: uint8(y * 6),
				B: uint8((x * y) % 256),
				A: 255,
			})
		}
	}

	processed := RenderToImage(img, 20, 10, Options{PaletteSize: N})
	bd := processed.Bounds()
	colors := make(map[[3]uint8]struct{})
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			p := processed.RGBAAt(x, y)
			colors[[3]uint8{p.R, p.G, p.B}] = struct{}{}
		}
	}

	if len(colors) > N {
		t.Errorf("median-cut: want ≤ %d distinct colors, got %d", N, len(colors))
	}
}

// TestPlaceholderDeterminism checks that the same seed always produces the same
// output, while different seeds produce different output.
func TestPlaceholderDeterminism(t *testing.T) {
	const cols, rows = 20, 10
	a1 := Placeholder("seed-alpha", cols, rows, Options{})
	a2 := Placeholder("seed-alpha", cols, rows, Options{})
	b1 := Placeholder("seed-beta", cols, rows, Options{})

	if a1 != a2 {
		t.Error("Placeholder: same seed produced different output (not deterministic)")
	}
	if a1 == b1 {
		t.Error("Placeholder: different seeds produced identical output")
	}
}

// TestPlaceholderHorizontalMirror checks that the raw sprite image produced by
// PlaceholderImage is horizontally mirrored: pixel (x,y) == pixel (W-1-x, y)
// for all x in [0, W/2).
func TestPlaceholderHorizontalMirror(t *testing.T) {
	const cols, rows = 20, 10
	img := PlaceholderImage("mirror-test-seed", cols, rows, Options{})
	W := img.Bounds().Dx()
	H := img.Bounds().Dy()

	for y := 0; y < H; y++ {
		for x := 0; x < W/2; x++ {
			left := img.RGBAAt(x, y)
			right := img.RGBAAt(W-1-x, y)
			if left != right {
				t.Errorf("y=%d x=%d: left %v != right %v (mirror broken)", y, x, left, right)
			}
		}
	}
}

// TestPlaceholderWithPalette checks that Placeholder runs without error when a
// palette is supplied, and still produces deterministic results.
func TestPlaceholderWithPalette(t *testing.T) {
	palette := []color.Color{
		color.RGBA{R: 255, G: 0, B: 128, A: 255},
		color.RGBA{R: 0, G: 128, B: 255, A: 255},
	}
	o := Options{Palette: palette}

	s1 := Placeholder("pal-seed", 16, 8, o)
	s2 := Placeholder("pal-seed", 16, 8, o)
	if s1 != s2 {
		t.Error("Placeholder with palette: not deterministic")
	}
}

// TestRewriteThumbURL is a table-driven test covering all documented URL forms.
func TestRewriteThumbURL(t *testing.T) {
	const px = 512

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "replace w-h-l suffix (googleusercontent)",
			in:   "https://lh3.googleusercontent.com/someImageHash=w544-h544-l90-rj",
			want: "https://lh3.googleusercontent.com/someImageHash=w512-h512-l90-rj",
		},
		{
			name: "replace s suffix (ggpht)",
			in:   "https://yt3.ggpht.com/ytc/imageHash=s256",
			want: "https://yt3.ggpht.com/ytc/imageHash=w512-h512-l90-rj",
		},
		{
			name: "append when no equals segment",
			in:   "https://lh3.googleusercontent.com/someImageHash",
			want: "https://lh3.googleusercontent.com/someImageHash=w512-h512-l90-rj",
		},
		{
			name: "passthrough non-google host",
			in:   "https://i.ytimg.com/vi/dQw4w9WgXcQ/mqdefault.jpg",
			want: "https://i.ytimg.com/vi/dQw4w9WgXcQ/mqdefault.jpg",
		},
		{
			name: "bare ggpht.com host",
			in:   "https://ggpht.com/someImage=w100-h100",
			want: "https://ggpht.com/someImage=w512-h512-l90-rj",
		},
		{
			name: "bare googleusercontent.com host (no subdomain)",
			in:   "https://googleusercontent.com/img=s800",
			want: "https://googleusercontent.com/img=w512-h512-l90-rj",
		},
		{
			name: "empty URL passthrough",
			in:   "",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RewriteThumbURL(tc.in, px)
			if got != tc.want {
				t.Errorf("RewriteThumbURL(%q, %d)\n  got  %q\n  want %q", tc.in, px, got, tc.want)
			}
		})
	}
}

// TestHSLToRGB spot-checks a handful of known HSL→RGB conversions.
func TestHSLToRGB(t *testing.T) {
	tests := []struct {
		h, s, l float64
		r, g, b uint8
		name    string
	}{
		{h: 0, s: 1, l: 0.5, r: 255, g: 0, b: 0, name: "red"},
		{h: 120, s: 1, l: 0.5, r: 0, g: 255, b: 0, name: "green"},
		{h: 240, s: 1, l: 0.5, r: 0, g: 0, b: 255, name: "blue"},
		{h: 0, s: 0, l: 0.5, r: 128, g: 128, b: 128, name: "grey"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b := hslToRGB(tc.h, tc.s, tc.l)
			if r != tc.r || g != tc.g || b != tc.b {
				t.Errorf("hslToRGB(%v,%v,%v) = (%d,%d,%d), want (%d,%d,%d)",
					tc.h, tc.s, tc.l, r, g, b, tc.r, tc.g, tc.b)
			}
		})
	}
}

// TestCenterCropAspect verifies that centerCrop produces an image with the
// correct aspect ratio and centred origin.
func TestCenterCropAspect(t *testing.T) {
	// Wide source → should crop width to match square target.
	src := solidImage(200, 100, color.RGBA{255, 255, 255, 255})
	cropped := centerCrop(src, 10, 10) // square target
	if cropped.Bounds().Dx() != cropped.Bounds().Dy() {
		t.Errorf("expected square crop, got %v", cropped.Bounds())
	}
	// Tall source → should crop height to match wide target.
	// Call with tw=20, th=10 (matching cols=20, 2*rows=10) → 2:1 ratio.
	src2 := solidImage(100, 200, color.RGBA{0, 0, 0, 255})
	cropped2 := centerCrop(src2, 20, 10)
	b := cropped2.Bounds()
	// Dx/Dy should equal tw/th = 20/10 = 2, i.e. Dx*10 == Dy*20.
	if b.Dx()*10 != b.Dy()*20 {
		t.Errorf("expected 2:1 aspect (Dx*10==Dy*20), got %dx%d", b.Dx(), b.Dy())
	}
}

// TestAlphaComposite verifies that semi-transparent pixels are composited over
// black (not preserved as transparent).
func TestAlphaComposite(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	// Half-transparent white → should become grey (128,128,128)
	src.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 128})
	src.SetRGBA(1, 0, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	src.SetRGBA(0, 1, color.RGBA{R: 0, G: 0, B: 0, A: 0}) // fully transparent → black
	src.SetRGBA(1, 1, color.RGBA{R: 100, G: 100, B: 100, A: 200})

	processed := RenderToImage(src, 2, 1, Options{})
	// All output pixels must have A==255.
	bd := processed.Bounds()
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			if p := processed.RGBAAt(x, y); p.A != 255 {
				t.Errorf("pixel (%d,%d) has A=%d, want 255", x, y, p.A)
			}
		}
	}
}
