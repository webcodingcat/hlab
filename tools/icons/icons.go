package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/tc-hib/winres"
)

// The geometry is drawn on a grid of unit×unit; every output scales it.
const unit = 256.0

// rect is an axis-aligned rectangle on the grid.
type rect struct{ x0, y0, x1, y1 float64 }

var (
	// background is the rounded square, filling the whole icon.
	background = rect{0, 0, unit, unit}
	radius     = 56.0
	// letter is the white "H": two legs and the crossbar.
	letter = []rect{
		{64, 56, 104, 200},
		{152, 56, 192, 200},
		{104, 110, 152, 146},
	}
)

// Colours from UI-SPEC "Colours" (light mode values).
var (
	accent = color.NRGBA{0x2f, 0x6f, 0xdf, 0xff}
	white  = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	states = []struct {
		name string
		dot  color.NRGBA
	}{
		{"running", color.NRGBA{0x1f, 0x9d, 0x55, 0xff}}, // success
		{"busy", color.NRGBA{0xc9, 0x8a, 0x0a, 0xff}},    // warning
		{"stopped", color.NRGBA{0x5d, 0x64, 0x72, 0xff}}, // text muted
		{"error", color.NRGBA{0xd0, 0x34, 0x3a, 0xff}},   // error
	}
)

var (
	appSizes   = []int{16, 20, 24, 32, 48, 256}
	stateSizes = []int{16, 20, 24, 32}
	programs   = []string{"hlab-tray", "hlab", "hlabd"}
)

// Icon resource IDs: 3 is the icon Wails loads for windows and dialogs,
// 32512 (IDI_APPLICATION) the one it loads for its window class.
var groupIDs = []winres.ID{3, 32512}

// Generate writes the icons, the .syso resources and the SVGs under root.
func Generate(root string) error {
	app := map[int]*image.NRGBA{}
	for _, s := range appSizes {
		app[s] = drawApp(s)
	}
	appICO, err := encodeICO(app, appSizes)
	if err != nil {
		return err
	}
	iconDir := filepath.Join(root, "cmd", "hlab-tray", "icons")
	if err := write(filepath.Join(iconDir, "app.ico"), appICO); err != nil {
		return err
	}
	for _, st := range states {
		frames := map[int]*image.NRGBA{}
		for _, s := range stateSizes {
			frames[s] = withDot(app[s], st.dot)
		}
		ico, err := encodeICO(frames, stateSizes)
		if err != nil {
			return err
		}
		if err := write(filepath.Join(iconDir, st.name+".ico"), ico); err != nil {
			return err
		}
	}

	syso, err := resources(appICO)
	if err != nil {
		return err
	}
	for _, p := range programs {
		if err := write(filepath.Join(root, "cmd", p, "rsrc_windows_amd64.syso"), syso); err != nil {
			return err
		}
	}

	if err := write(filepath.Join(root, "ui", "frontend", "src", "assets", "app-icon.svg"), []byte(appSVG())); err != nil {
		return err
	}
	return write(filepath.Join(root, "docs", "images", "banner.svg"), []byte(bannerSVG()))
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// samples is the number of samples per pixel and axis of the
// supersampled anti-aliasing.
func samples(size int) int {
	if size >= 128 {
		return 4
	}
	return 8
}

// coverage returns the share of the pixel (x, y) inside shape, sampled on
// an n×n grid.
func coverage(x, y, n int, inside func(px, py float64) bool) float64 {
	hit := 0
	for j := range n {
		for i := range n {
			if inside(float64(x)+(float64(i)+0.5)/float64(n), float64(y)+(float64(j)+0.5)/float64(n)) {
				hit++
			}
		}
	}
	return float64(hit) / float64(n*n)
}

// premul is a premultiplied colour with components in 0..1.
type premul struct{ r, g, b, a float64 }

func toPremul(c color.NRGBA) premul {
	a := float64(c.A) / 255
	return premul{float64(c.R) / 255 * a, float64(c.G) / 255 * a, float64(c.B) / 255 * a, a}
}

// over paints c with coverage k over dst.
func over(dst premul, c color.NRGBA, k float64) premul {
	s := toPremul(c)
	s = premul{s.r * k, s.g * k, s.b * k, s.a * k}
	return premul{s.r + dst.r*(1-s.a), s.g + dst.g*(1-s.a), s.b + dst.b*(1-s.a), s.a + dst.a*(1-s.a)}
}

func toNRGBA(p premul) color.NRGBA {
	if p.a <= 0 {
		return color.NRGBA{}
	}
	ch := func(v float64) uint8 { return uint8(math.Round(math.Min(1, v/p.a) * 255)) }
	return color.NRGBA{ch(p.r), ch(p.g), ch(p.b), uint8(math.Round(p.a * 255))}
}

func inRoundedRect(r rect, rad float64) func(x, y float64) bool {
	return func(x, y float64) bool {
		if x < r.x0 || x >= r.x1 || y < r.y0 || y >= r.y1 {
			return false
		}
		cx := math.Max(r.x0+rad, math.Min(x, r.x1-rad))
		cy := math.Max(r.y0+rad, math.Min(y, r.y1-rad))
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad
	}
}

func inRect(r rect) func(x, y float64) bool {
	return func(x, y float64) bool { return x >= r.x0 && x < r.x1 && y >= r.y0 && y < r.y1 }
}

func inCircle(cx, cy, r float64) func(x, y float64) bool {
	return func(x, y float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r }
}

func scale(r rect, f float64) rect { return rect{r.x0 * f, r.y0 * f, r.x1 * f, r.y1 * f} }

// drawApp renders the application icon at size px.
func drawApp(size int) *image.NRGBA {
	f := float64(size) / unit
	bg := inRoundedRect(scale(background, f), radius*f)
	var legs []func(x, y float64) bool
	for _, r := range letter {
		legs = append(legs, inRect(scale(r, f)))
	}
	inLetter := func(x, y float64) bool {
		for _, in := range legs {
			if in(x, y) {
				return true
			}
		}
		return false
	}
	n := samples(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			p := over(premul{}, accent, coverage(x, y, n, bg))
			p = over(p, white, coverage(x, y, n, inLetter))
			img.SetNRGBA(x, y, toNRGBA(p))
		}
	}
	return img
}

// withDot returns a copy of app with the status dot in the lower-right
// corner: a third of the icon wide plus a 1 px white rim. Pixels the dot
// does not touch stay exactly as in app.
func withDot(app *image.NRGBA, c color.NRGBA) *image.NRGBA {
	size := app.Rect.Dx()
	inner := float64(size) * 0.375 / 2
	outer := inner + 1
	cx, cy := float64(size)-outer, float64(size)-outer
	rim, fill := inCircle(cx, cy, outer), inCircle(cx, cy, inner)
	n := samples(size)
	img := image.NewNRGBA(app.Rect)
	copy(img.Pix, app.Pix)
	for y := range size {
		for x := range size {
			kr := coverage(x, y, n, rim)
			if kr == 0 {
				continue
			}
			p := over(toPremul(app.NRGBAAt(x, y)), white, kr)
			p = over(p, c, coverage(x, y, n, fill))
			img.SetNRGBA(x, y, toNRGBA(p))
		}
	}
	return img
}

// encodeICO writes an ICO file with one frame per size: 32-bit BMP frames,
// a PNG frame for 256 px.
func encodeICO(frames map[int]*image.NRGBA, sizes []int) ([]byte, error) {
	var data [][]byte
	for _, s := range sizes {
		img := frames[s]
		if s >= 256 {
			var b bytes.Buffer
			if err := png.Encode(&b, img); err != nil {
				return nil, err
			}
			data = append(data, b.Bytes())
			continue
		}
		data = append(data, bmpFrame(img))
	}
	var out bytes.Buffer
	le := func(v any) { _ = binary.Write(&out, binary.LittleEndian, v) }
	le([3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s % 256) // 0 means 256
		le([4]uint8{dim, dim, 0, 0})
		le([2]uint16{1, 32})
		le([2]uint32{uint32(len(data[i])), uint32(offset)})
		offset += len(data[i])
	}
	for _, d := range data {
		out.Write(d)
	}
	return out.Bytes(), nil
}

// bmpFrame encodes img as an ICO bitmap: BITMAPINFOHEADER with the double
// height, bottom-up BGRA rows, then an all-zero AND mask.
func bmpFrame(img *image.NRGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	maskRow := (w + 31) / 32 * 4
	var out bytes.Buffer
	le := func(v any) { _ = binary.Write(&out, binary.LittleEndian, v) }
	le(uint32(40))
	le([2]int32{int32(w), int32(2 * h)})
	le([2]uint16{1, 32})
	le([6]uint32{0, uint32(w*h*4 + maskRow*h), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- {
		for x := range w {
			c := img.NRGBAAt(x, y)
			out.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	out.Write(make([]byte, maskRow*h))
	return out.Bytes()
}

// resources builds the COFF object with the icon group under every ID of
// groupIDs, sharing one set of icon images.
func resources(ico []byte) ([]byte, error) {
	icon, err := winres.LoadICO(bytes.NewReader(ico))
	if err != nil {
		return nil, err
	}
	rs := &winres.ResourceSet{}
	if err := rs.SetIcon(groupIDs[0], icon); err != nil {
		return nil, err
	}
	group := rs.Get(winres.RT_GROUP_ICON, groupIDs[0], winres.LCIDNeutral)
	for _, id := range groupIDs[1:] {
		if err := rs.Set(winres.RT_GROUP_ICON, id, winres.LCIDNeutral, group); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if err := rs.WriteObject(&out, winres.ArchAMD64); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func num(v float64) string { return fmt.Sprintf("%g", v) }

// iconSVG returns the icon's elements on the unit grid.
func iconSVG() string {
	s := fmt.Sprintf(`<rect width="%s" height="%s" rx="%s" fill="#%02x%02x%02x"/>`,
		num(unit), num(unit), num(radius), accent.R, accent.G, accent.B)
	s += `<path fill="#ffffff" d="`
	for _, r := range letter {
		s += fmt.Sprintf("M%s %sH%sV%sH%sZ", num(r.x0), num(r.y0), num(r.x1), num(r.y1), num(r.x0))
	}
	return s + `"/>`
}

func appSVG() string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s">%s</svg>`+"\n",
		num(unit), num(unit), num(unit), num(unit), iconSVG())
}

// bannerSVG is the README banner: the icon, "HLab" and the subtitle, with
// text colours from UI-SPEC for light and dark mode.
func bannerSVG() string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 720 200" width="720" height="200">
<style>
.title { fill: #1b1e24; font: 700 64px "Segoe UI", system-ui, sans-serif; }
.subtitle { fill: #5d6472; font: 400 24px "Segoe UI", system-ui, sans-serif; }
@media (prefers-color-scheme: dark) {
  .title { fill: #e6e8ec; }
  .subtitle { fill: #9aa1ad; }
}
</style>
<g transform="translate(24 36) scale(0.5)">` + iconSVG() + `</g>
<text class="title" x="180" y="112">HLab</text>
<text class="subtitle" x="182" y="152">A local Hetzner-compatible cloud on Hyper-V</text>
</svg>
`
}
