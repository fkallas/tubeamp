// Package art implements a half-block pixel-art renderer for terminal display.
// Each terminal cell holds two vertical pixels via '▀' (U+2580 UPPER HALF BLOCK):
// the foreground colour is the top pixel; the background colour is the bottom pixel.
// This packs cols×(2·rows) pixels into a cols×rows block of terminal cells.
package art

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Options controls the rendering pipeline applied by Render and Placeholder.
type Options struct {
	PaletteSize int           // >0: median-cut quantize to at most N colors
	Palette     []color.Color // non-nil: snap every pixel to nearest palette color (takes precedence over PaletteSize)
	Dither      bool          // apply Bayer 4×4 ordered dithering before palette mapping
}

// upperHalfBlock is U+2580 UPPER HALF BLOCK (▀).
const upperHalfBlock = "▀"

// bayer4x4 is the canonical 4×4 Bayer ordered-dithering threshold matrix.
// Values are in [0, 15]; normalised threshold = (v + 0.5) / 16.
var bayer4x4 = [4][4]int{
	{0, 8, 2, 10},
	{12, 4, 14, 6},
	{3, 11, 1, 9},
	{15, 7, 13, 5},
}

// maxFetchBytes is the maximum body size accepted by Fetch.
const maxFetchBytes = 5 << 20 // 5 MiB

// ── FNV-1a / xorshift PRNG ───────────────────────────────────────────────────

// fnv1a32 returns the FNV-1a 32-bit hash of s.
func fnv1a32(s string) uint32 {
	const (
		offset uint32 = 2166136261
		prime  uint32 = 16777619
	)
	h := offset
	for _, b := range []byte(s) {
		h ^= uint32(b)
		h *= prime
	}
	return h
}

// xorshift32 is a minimal 32-bit xorshift PRNG. Do NOT use math/rand or time.
type xorshift32 struct{ state uint32 }

func newXorshift32(seed uint32) *xorshift32 {
	if seed == 0 {
		seed = 1 // zero state produces an infinite stream of zeros
	}
	return &xorshift32{state: seed}
}

// next returns the next pseudo-random uint32.
func (x *xorshift32) next() uint32 {
	x.state ^= x.state << 13
	x.state ^= x.state >> 17
	x.state ^= x.state << 5
	return x.state
}

// ── Image helpers ─────────────────────────────────────────────────────────────

// clampU8 clamps v to [0, 255].
func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// toRGBA converts any image.Image to *image.RGBA with a (0,0) origin.
func toRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x-b.Min.X, y-b.Min.Y, img.At(x, y))
		}
	}
	return out
}

// centerCrop center-crops img to the target aspect ratio tw:th.
// When the source aspect equals the target no copying is done beyond
// returning a fresh image with a (0,0) origin.
func centerCrop(img *image.RGBA, tw, th int) *image.RGBA {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	if W == 0 || H == 0 || tw == 0 || th == 0 {
		return img
	}

	var x0, y0, newW, newH int
	// Compare W/H vs tw/th using integer cross-multiplication to avoid float.
	if W*th > H*tw { // source is wider → crop width
		newH = H
		newW = H * tw / th
		x0 = (W - newW) / 2
		y0 = 0
	} else { // source is taller (or exactly equal) → crop height
		newW = W
		newH = W * th / tw
		x0 = 0
		y0 = (H - newH) / 2
	}

	out := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			out.SetRGBA(x, y, img.RGBAAt(b.Min.X+x0+x, b.Min.Y+y0+y))
		}
	}
	return out
}

// scaleNN scales img to tw×th using nearest-neighbour interpolation.
func scaleNN(img *image.RGBA, tw, th int) *image.RGBA {
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	for ty := 0; ty < th; ty++ {
		sy := ty * sh / th
		for tx := 0; tx < tw; tx++ {
			sx := tx * sw / tw
			out.SetRGBA(tx, ty, img.RGBAAt(sb.Min.X+sx, sb.Min.Y+sy))
		}
	}
	return out
}

// compositeOverBlack alpha-composites every pixel over an opaque black background,
// producing an image with A=255 everywhere.
func compositeOverBlack(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			p := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			a := int(p.A)
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(int(p.R) * a / 255),
				G: uint8(int(p.G) * a / 255),
				B: uint8(int(p.B) * a / 255),
				A: 255,
			})
		}
	}
	return out
}

// applyBayerDithering applies a 4×4 Bayer ordered dither to img in-place.
// Each pixel channel is offset by a spatially-varying amount derived from the
// Bayer threshold matrix, spreading quantisation error across the image.
func applyBayerDithering(img *image.RGBA) {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			// t ∈ [0,15]; offset ∈ [-32, +28]
			t := bayer4x4[y%4][x%4]
			offset := (t - 8) * 4
			p := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			img.SetRGBA(b.Min.X+x, b.Min.Y+y, color.RGBA{
				R: clampU8(int(p.R) + offset),
				G: clampU8(int(p.G) + offset),
				B: clampU8(int(p.B) + offset),
				A: p.A,
			})
		}
	}
}

// nearestInPalette returns the RGBA from pal whose RGB values are nearest to
// (r, g, b) by squared Euclidean distance.
func nearestInPalette(r, g, b uint8, pal []color.RGBA) color.RGBA {
	if len(pal) == 0 {
		return color.RGBA{R: r, G: g, B: b, A: 255}
	}
	best := 0
	bestDist := math.MaxInt32
	for i, c := range pal {
		dr := int(r) - int(c.R)
		dg := int(g) - int(c.G)
		db := int(b) - int(c.B)
		d := dr*dr + dg*dg + db*db
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return pal[best]
}

// applyPalette snaps every pixel in img to the nearest color in palette by
// squared RGB distance.
func applyPalette(img *image.RGBA, palette []color.Color) {
	// Pre-convert to RGBA for fast lookup.
	pal := make([]color.RGBA, len(palette))
	for i, c := range palette {
		r, g, bv, _ := c.RGBA() // each channel is uint16 in [0, 65535]
		pal[i] = color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bv >> 8), A: 255}
	}

	bd := img.Bounds()
	W, H := bd.Dx(), bd.Dy()
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			p := img.RGBAAt(bd.Min.X+x, bd.Min.Y+y)
			n := nearestInPalette(p.R, p.G, p.B, pal)
			img.SetRGBA(bd.Min.X+x, bd.Min.Y+y, color.RGBA{R: n.R, G: n.G, B: n.B, A: p.A})
		}
	}
}

// ── Median-cut quantisation ───────────────────────────────────────────────────

// medianCutQuantize quantizes img to at most maxColors using the median-cut
// algorithm, then snaps every pixel to the resulting palette.
func medianCutQuantize(img *image.RGBA, maxColors int) {
	if maxColors <= 0 {
		return
	}
	bd := img.Bounds()
	W, H := bd.Dx(), bd.Dy()

	pixels := make([]color.RGBA, 0, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			pixels = append(pixels, img.RGBAAt(bd.Min.X+x, bd.Min.Y+y))
		}
	}

	palette := medianCut(pixels, maxColors)
	palC := make([]color.Color, len(palette))
	for i, c := range palette {
		palC[i] = c
	}
	applyPalette(img, palC)
}

// medianCut performs the median-cut algorithm on pixels, returning up to
// maxColors representative RGBA colours.
func medianCut(pixels []color.RGBA, maxColors int) []color.RGBA {
	if len(pixels) == 0 || maxColors <= 0 {
		return nil
	}
	buckets := [][]color.RGBA{pixels}

	for len(buckets) < maxColors {
		// Find the bucket with the widest color range.
		best := -1
		bestRange := 0
		for i, bk := range buckets {
			if r := bucketRange(bk); r > bestRange {
				bestRange = r
				best = i
			}
		}
		if best < 0 || bestRange == 0 {
			break
		}
		b1, b2 := splitBucket(buckets[best])
		// Replace the chosen bucket with its two halves.
		last := len(buckets) - 1
		buckets[best] = buckets[last]
		buckets = buckets[:last]
		buckets = append(buckets, b1, b2)
	}

	out := make([]color.RGBA, len(buckets))
	for i, bk := range buckets {
		out[i] = averageColor(bk)
	}
	return out
}

// bucketRange returns the largest per-channel range across R, G, B.
func bucketRange(pixels []color.RGBA) int {
	if len(pixels) == 0 {
		return 0
	}
	minR, maxR := pixels[0].R, pixels[0].R
	minG, maxG := pixels[0].G, pixels[0].G
	minB, maxB := pixels[0].B, pixels[0].B
	for _, p := range pixels[1:] {
		if p.R < minR {
			minR = p.R
		}
		if p.R > maxR {
			maxR = p.R
		}
		if p.G < minG {
			minG = p.G
		}
		if p.G > maxG {
			maxG = p.G
		}
		if p.B < minB {
			minB = p.B
		}
		if p.B > maxB {
			maxB = p.B
		}
	}
	rr := int(maxR) - int(minR)
	rg := int(maxG) - int(minG)
	rb := int(maxB) - int(minB)
	if rr >= rg && rr >= rb {
		return rr
	}
	if rg >= rb {
		return rg
	}
	return rb
}

// splitBucket sorts a copy of pixels by the channel with the widest range and
// splits it at the median, returning two halves.
func splitBucket(pixels []color.RGBA) ([]color.RGBA, []color.RGBA) {
	if len(pixels) == 0 {
		return nil, nil
	}
	cp := make([]color.RGBA, len(pixels))
	copy(cp, pixels)

	minR, maxR := cp[0].R, cp[0].R
	minG, maxG := cp[0].G, cp[0].G
	minB, maxB := cp[0].B, cp[0].B
	for _, p := range cp[1:] {
		if p.R < minR {
			minR = p.R
		}
		if p.R > maxR {
			maxR = p.R
		}
		if p.G < minG {
			minG = p.G
		}
		if p.G > maxG {
			maxG = p.G
		}
		if p.B < minB {
			minB = p.B
		}
		if p.B > maxB {
			maxB = p.B
		}
	}
	rr := int(maxR) - int(minR)
	rg := int(maxG) - int(minG)
	rb := int(maxB) - int(minB)

	switch {
	case rr >= rg && rr >= rb:
		sort.Slice(cp, func(i, j int) bool { return cp[i].R < cp[j].R })
	case rg >= rb:
		sort.Slice(cp, func(i, j int) bool { return cp[i].G < cp[j].G })
	default:
		sort.Slice(cp, func(i, j int) bool { return cp[i].B < cp[j].B })
	}

	mid := len(cp) / 2
	return cp[:mid], cp[mid:]
}

// averageColor returns the per-channel mean of pixels (A is always 255).
func averageColor(pixels []color.RGBA) color.RGBA {
	if len(pixels) == 0 {
		return color.RGBA{A: 255}
	}
	var r, g, b int
	for _, p := range pixels {
		r += int(p.R)
		g += int(p.G)
		b += int(p.B)
	}
	n := len(pixels)
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 255}
}

// ── Half-block rendering ──────────────────────────────────────────────────────

// hexColor formats an (r,g,b) triplet as a lipgloss hex colour string.
func hexColor(r, g, b uint8) lipgloss.Color {
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

// renderHalfBlocks converts a cols×(2*rows) RGBA image to rows newline-separated
// lines of lipgloss-styled UPPER HALF BLOCK characters (U+2580).
func renderHalfBlocks(img *image.RGBA, cols, rows int) string {
	var sb strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			sb.WriteByte('\n')
		}
		for col := 0; col < cols; col++ {
			top := img.RGBAAt(col, row*2)
			bot := img.RGBAAt(col, row*2+1)
			cell := lipgloss.NewStyle().
				Foreground(hexColor(top.R, top.G, top.B)).
				Background(hexColor(bot.R, bot.G, bot.B)).
				Render(upperHalfBlock)
			sb.WriteString(cell)
		}
	}
	return sb.String()
}

// processImage applies the full pipeline: center-crop → nearest-neighbour scale
// → alpha-composite over black → optional Bayer dither → optional palette snap
// or median-cut quantisation. Returns a fresh cols×(2*rows) RGBA image.
func processImage(img image.Image, cols, rows int, o Options) *image.RGBA {
	rgba := toRGBA(img)
	cropped := centerCrop(rgba, cols, 2*rows)
	scaled := scaleNN(cropped, cols, 2*rows)
	composited := compositeOverBlack(scaled)
	if o.Dither {
		applyBayerDithering(composited)
	}
	if o.Palette != nil {
		applyPalette(composited, o.Palette)
	} else if o.PaletteSize > 0 {
		medianCutQuantize(composited, o.PaletteSize)
	}
	return composited
}

// Render center-crops img to the target pixel aspect (cols : 2*rows), scales
// to cols×(2*rows) using nearest-neighbour interpolation, composites alpha over
// black, optionally dithers and/or quantises, then returns rows lines of
// lipgloss-styled UPPER HALF BLOCK characters joined with '\n'.
func Render(img image.Image, cols, rows int, o Options) string {
	processed := processImage(img, cols, rows, o)
	return renderHalfBlocks(processed, cols, rows)
}

// RenderToImage applies the same pipeline as Render but returns the processed
// cols×(2*rows) RGBA image instead of a styled string. This is exported to
// allow tests to inspect pixel colours without parsing ANSI escape codes.
func RenderToImage(img image.Image, cols, rows int, o Options) *image.RGBA {
	return processImage(img, cols, rows, o)
}

// ── Placeholder ───────────────────────────────────────────────────────────────

// hslToRGB converts HSL (h ∈ [0,360), s ∈ [0,1], l ∈ [0,1]) to sRGB.
func hslToRGB(h, s, l float64) (r, g, b uint8) {
	c := (1.0 - math.Abs(2.0*l-1.0)) * s
	x := c * (1.0 - math.Abs(math.Mod(h/60.0, 2.0)-1.0))
	m := l - c/2.0

	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}

	r = uint8(math.Round((r1 + m) * 255))
	g = uint8(math.Round((g1 + m) * 255))
	b = uint8(math.Round((b1 + m) * 255))
	return
}

// PlaceholderImage builds the raw procedural sprite image for seed without
// applying half-block rendering. It is exported so that tests can inspect the
// pixel grid (e.g. to verify horizontal mirroring) without parsing ANSI codes.
func PlaceholderImage(seed string, cols, rows int, o Options) *image.RGBA {
	W, H := cols, 2*rows
	img := image.NewRGBA(image.Rect(0, 0, W, H))

	rng := newXorshift32(fnv1a32(seed))

	// Choose 2–3 foreground colors.
	nColors := 2 + int(rng.next()%2) // 2 or 3
	fgColors := make([]color.RGBA, 0, nColors)
	if len(o.Palette) > 0 {
		for i := 0; i < nColors; i++ {
			idx := rng.next() % uint32(len(o.Palette))
			rv, gv, bv, _ := o.Palette[idx].RGBA()
			fgColors = append(fgColors, color.RGBA{R: uint8(rv >> 8), G: uint8(gv >> 8), B: uint8(bv >> 8), A: 255})
		}
	} else {
		for i := 0; i < nColors; i++ {
			hue := float64(rng.next() % 360)
			rv, gv, bv := hslToRGB(hue, 0.7, 0.6)
			fgColors = append(fgColors, color.RGBA{R: rv, G: gv, B: bv, A: 255})
		}
	}

	bg := color.RGBA{R: 10, G: 10, B: 20, A: 255}
	leftW := W / 2

	// Generate the left half: ~45% of pixels are "on" (foreground).
	type cell struct {
		on  bool
		idx int
	}
	left := make([]cell, leftW*H)
	for i := range left {
		if rng.next()%100 < 45 {
			left[i] = cell{on: true, idx: int(rng.next() % uint32(len(fgColors)))}
		}
	}

	// Place pixels — left half and its mirror.
	for y := 0; y < H; y++ {
		for x := 0; x < leftW; x++ {
			c := left[y*leftW+x]
			if c.on {
				pix := fgColors[c.idx]
				img.SetRGBA(x, y, pix)
				img.SetRGBA(W-1-x, y, pix)
			} else {
				img.SetRGBA(x, y, bg)
				img.SetRGBA(W-1-x, y, bg)
			}
		}
	}

	// For odd widths the center column has no mirror; fill it independently.
	if W%2 != 0 {
		cx := W / 2
		for y := 0; y < H; y++ {
			if rng.next()%100 < 45 {
				pix := fgColors[int(rng.next()%uint32(len(fgColors)))]
				img.SetRGBA(cx, y, pix)
			} else {
				img.SetRGBA(cx, y, bg)
			}
		}
	}

	return img
}

// Placeholder generates a deterministic procedural cover for tracks without art.
// It seeds a tiny xorshift PRNG from the FNV-1a hash of seed, fills the left
// half of a cols×(2*rows) pixel grid at ~45% density, mirrors it horizontally
// (space-invader silhouette), then calls Render for consistent half-block styling.
// Foreground colours are drawn from o.Palette when non-empty; otherwise they are
// derived from the hash via HSL→RGB conversion. Background is near-black.
func Placeholder(seed string, cols, rows int, o Options) string {
	img := PlaceholderImage(seed, cols, rows, o)
	// The image is already cols×(2*rows) with A=255, so crop/scale/composite
	// in Render are no-ops; only dithering and palette mapping are applied.
	return Render(img, cols, rows, o)
}

// ── RewriteThumbURL ───────────────────────────────────────────────────────────

// RewriteThumbURL rewrites googleusercontent.com and ggpht.com thumbnail URLs
// to request px×px images by replacing (or appending) the trailing "=..." size
// segment with "=w<px>-h<px>-l90-rj". All other URLs are returned unchanged.
//
// Google image-serving uses a bare "=" (not "?...=") as a separator for size
// parameters embedded in the URL path, e.g. "=w544-h544-l90-rj" or "=s256".
func RewriteThumbURL(raw string, px int) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := u.Hostname()
	isGoogle := strings.HasSuffix(host, ".googleusercontent.com") ||
		host == "googleusercontent.com" ||
		strings.HasSuffix(host, ".ggpht.com") ||
		host == "ggpht.com"
	if !isGoogle {
		return raw
	}

	suffix := fmt.Sprintf("=w%d-h%d-l90-rj", px, px)

	if i := strings.LastIndex(raw, "="); i >= 0 {
		return raw[:i] + suffix
	}
	return raw + suffix
}

// ── Fetch ─────────────────────────────────────────────────────────────────────

// Fetch downloads url using a "tubeamp/0.1" User-Agent, caps the response body
// at 5 MiB, and decodes the image with the registered JPEG and PNG decoders.
func Fetch(ctx context.Context, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("art.Fetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", "tubeamp/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("art.Fetch: http: %w", err)
	}
	defer resp.Body.Close()

	img, _, err := image.Decode(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return nil, fmt.Errorf("art.Fetch: decode: %w", err)
	}
	return img, nil
}
