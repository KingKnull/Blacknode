// icongen renders frontend/public/icon.svg to build/appicon.png at 1024×1024.
// Run after editing the SVG; then `wails3 generate icons -input build/appicon.png`
// to produce the platform-specific .ico/.icns files Wails uses at build time.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

func main() {
	in := flag.String("in", "frontend/public/icon.svg", "input SVG path")
	out := flag.String("out", "build/appicon.png", "output PNG path")
	size := flag.Int("size", 1024, "output square pixel size")
	flag.Parse()

	icon, err := oksvg.ReadIcon(*in, oksvg.WarnErrorMode)
	if err != nil {
		log.Fatalf("read svg: %v", err)
	}
	icon.SetTarget(0, 0, float64(*size), float64(*size))

	rgba := image.NewRGBA(image.Rect(0, 0, *size, *size))
	scanner := rasterx.NewScannerGV(*size, *size, rgba, rgba.Bounds())
	icon.Draw(rasterx.NewDasher(*size, *size, scanner), 1.0)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create %s: %v", *out, err)
	}
	defer f.Close()
	if err := png.Encode(f, rgba); err != nil {
		log.Fatalf("encode png: %v", err)
	}
	fmt.Printf("wrote %s (%dx%d)\n", *out, *size, *size)
}
