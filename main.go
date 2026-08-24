package main

import (
	"embed"
	"encoding/json/v2"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/timewasted/go-accept-headers"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

//go:embed static/*
var static embed.FS

func Rect(ra *vector.Rasterizer, x, y, width, height float32) {
	ra.MoveTo(x, y)
	ra.LineTo(x+width, y)
	ra.LineTo(x+width, y+height)
	ra.LineTo(x, y+height)
	ra.ClosePath()
}

func RoundedRect(ra *vector.Rasterizer, x, y, width, height, r float32) {
	ct := r * 0.552
	ra.MoveTo(x+r, y)
	ra.CubeTo(x+r-ct, y, x, y+r-ct, x, y+r)
	ra.LineTo(x, y+height-r)
	ra.CubeTo(x, y+height-r+ct, x+r-ct, y+height, x+r, y+height)
	ra.LineTo(x+width-r, y+height)
	ra.CubeTo(x+width-r+ct, y+height, x+width, y+height-r+ct, x+width, y+height-r)
	ra.LineTo(x+width, y+r)
	ra.CubeTo(x+width, y+r-ct, x+width-r+ct, y, x+width-r, y)
	ra.ClosePath()
}

type Gradient struct{ int }

func (g Gradient) At(x, y int) color.Color {
	out := 1
	if x*2 > g.int {
		out ^= x & 1
	}
	if y*2 > g.int {
		out ^= y & 1
	}
	if out == 0 {
		return color.Transparent
	} else {
		return color.Opaque
	}
}

func (g Gradient) Bounds() image.Rectangle {
	return (*image.Uniform)(nil).Bounds()
}

func (g Gradient) ColorModel() color.Model {
	return image.Transparent
}

func MakeTile(label string, scale int, col color.Color) image.Image {
	sizei := 256 * scale
	size := float32(sizei)
	ra := vector.NewRasterizer(sizei, sizei)
	inset := size / 16
	rad := inset * 2

	// 1px white border with transparent inside
	im := image.NewRGBA(image.Rect(0, 0, sizei, sizei))
	draw.Draw(im, image.Rect(0, 0, sizei, sizei), image.White, image.Point{}, draw.Over)
	draw.Draw(im, image.Rect(1, 1, sizei-1, sizei-1), image.Transparent, image.Point{}, draw.Src)

	Rect(ra, 1, 1, size-2, size-2)
	RoundedRect(ra, inset, inset, size-inset-inset, size-inset-inset, rad)

	// define the gradient mask
	fill := image.NewUniform(color.RGBA{210, 105, 30, 255})
	mask := Gradient{sizei}
	comb := image.NewRGBA(im.Bounds())
	draw.DrawMask(comb, im.Bounds(), fill, image.Pt(0, 0), mask, image.Pt(0, 0), draw.Src)

	// draw the picture frame
	ra.Draw(im, ra.Bounds(), comb, image.Pt(0, 0))

	// draw the label
	tm := fixed.P(3, 13)
	for _, c := range label {
		dr, mask, maskp, advance, ok := basicfont.Face7x13.Glyph(tm, c)
		if !ok {
			panic("")
		}
		draw.DrawMask(im, dr, image.NewUniform(color.White), image.Point{}, mask, maskp, draw.Over)
		tm = tm.Add(fixed.Point26_6{X: advance, Y: 0})
	}

	return im
}

func ServeTile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	z, err := strconv.Atoi(r.PathValue("z"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	x, err := strconv.Atoi(r.PathValue("x"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	yext := r.PathValue("yext")
	var y int
	if strings.HasSuffix(yext, ".png") {
		y, err = strconv.Atoi(strings.TrimSuffix(yext, ".png"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
	} else {
		http.NotFound(w, r)
		return
	}

	label := fmt.Sprintf("%d/%d/%d", z, x, y)

	im := MakeTile(label, 1, color.Black)
	err = png.Encode(w, im)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

type TileJson = struct {
	// 3.0.0
	Tilejson string `json:"tilejson"`
	// anything
	Name string `json:"name,omitempty"`
	// anything
	Description string `json:"description,omitempty"`
	// anything
	Version string `json:"version,omitempty"`
	// array of urls with {z}, {x}, {y} placeholders,
	// also {s} ("switch"), {ratio} (or {r}? empty or "2x"),
	// {quadkey}, {bbox-epsg-3857} ?
	Tiles []string `json:"tiles"`
	// 0
	Minzoom int `json:"minzoom"`
	// default is 22? 30? idk lol
	Maxzoom     int    `json:"maxzoom"`
	Attribution string `json:"attribution,omitempty"`
	// [lonmin, latmin, lonmax, latmax]
	Bounds []int `json:"bounds,omitempty"`
	// [lon, lat, z]
	Center []int `json:"center,omitempty"`
	// xyz (default) or tms
	Scheme string `json:"scheme,omitempty"`
	// 512 default? or 256
	TileSize int `json:"tileSize,omitempty"`
	// "terrarium" or "mapbox"
	Encoding string   `json:"encoding,omitempty"`
	Template string   `json:"template,omitempty"`
	Legend   string   `json:"legend,omitempty"`
	Grids    []string `json:"grids,omitempty"`
}

func DispatchIndex(w http.ResponseWriter, r *http.Request) {
	t, err := accept.Negotiate(r.Header.Get("Accept"), "application/json", "text/html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch t {
	case "application/json":
		ServeTileJson(w, r)
	case "text/html":
		ServeIndexPage(w, r)
	default:
		http.Error(w, http.StatusText(http.StatusNotAcceptable), http.StatusNotAcceptable)
	}
}

func ServeTileJson(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	origin := fmt.Sprintf("%s://%s", scheme, host)
	url := fmt.Sprint(origin, "/{z}/{x}/{y}.png")
	tj := TileJson{
		Tilejson: "3.0.0",
		Tiles:    []string{url},
		Maxzoom:  30,
		TileSize: 256,
	}
	if err := json.MarshalWrite(w, tj); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func ServeIndexPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	content, _ := static.ReadFile("static/index.html")
	w.Write(content)
	info, ok := debug.ReadBuildInfo()
	if ok {
		fmt.Fprintf(w, "<address>%s@%s</address>\n", info.Main.Path, info.Main.Version)
	}
}

func main() {
	http.HandleFunc("GET /{$}", DispatchIndex)
	http.HandleFunc("GET /{z}/{x}/{yext}", ServeTile)
	root, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	http.Handle("GET /", http.FileServerFS(root))
	port := fmt.Sprintf(":%s", os.Getenv("PORT"))
	listen, err := net.Listen("tcp", port)
	if err != nil {
		panic(err)
	}
	fmt.Printf("http://%s\n", listen.Addr())
	panic(http.Serve(listen, nil))
}
