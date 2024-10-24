package main

import (
	"embed"
	"encoding/json"
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

func MakeTile(label string, scale int, col color.Color) image.Image {
	sizei := 256 * scale
	size := float32(sizei)
	ra := vector.NewRasterizer(sizei, sizei)
	inset := size / 16
	rad := inset * 2

	// 1px white border with transparent inside
	im := image.NewRGBA(image.Rect(0, 0, sizei, sizei))
	draw.Draw(im, image.Rect(0, 0, sizei, sizei), image.NewUniform(color.White), image.Point{}, draw.Over)
	draw.Draw(im, image.Rect(1, 1, sizei-1, sizei-1), image.NewUniform(color.Transparent), image.Point{}, draw.Src)

	Rect(ra, 1, 1, size-2, size-2)
	RoundedRect(ra, inset, inset, size-inset-inset, size-inset-inset, rad)

	ra.Draw(im, ra.Bounds(), image.NewUniform(color.RGBA{210, 105, 30, 255}), image.Pt(0, 0))

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

func ServeTileJson(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Accept"), "text/html") {
		ServeIndexPage(w, r)
		return
	}
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
	out, err := json.Marshal(tj)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write(out)
}

func ServeIndexPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	content, _ := static.ReadFile("static/index.html")
	w.Write(content)
	fmt.Fprintf(w, "<address>%s</address>", footer())
}

func footer() string {
	path := ""
	revision := ""
	dirty := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		path = info.Path + "@"
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value[:12]
			case "vcs.modified":
				if setting.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		if revision == "" {
			return path + info.Main.Version
		}
	}
	return path + revision + dirty
}

func main() {
	http.HandleFunc("GET /{$}", ServeTileJson)
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
	http.Serve(listen, nil)
}
