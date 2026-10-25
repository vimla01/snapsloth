package main

import (
	"bytes"
	"image"
	_ "image/jpeg" // register decoders for image.Decode
	"image/png"
)

// defaultImageSize trades a little OCR sharpness for speed. Measured on a
// 1920x1080 screenshot, CPU only: full size is 932 image tokens (149 s),
// 1024px is 264 (31 s), 768px is 156 (18 s).
const defaultImageSize = 1024

// shrinkForModel scales a screenshot down so its longest side is at most
// maxSide. The vision model turns pixels into tokens, and on a CPU those
// tokens are most of the wait: a full-HD screenshot costs ~900 of them.
// Formats the standard library can't decode (webp) are sent unchanged.
func shrinkForModel(data []byte, maxSide int) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxSide <= 0 || max(w, h) <= maxSide {
		return data
	}
	nw, nh := w*maxSide/max(w, h), h*maxSide/max(w, h)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))

	// Box filter: each new pixel is the average of the source pixels it
	// covers, which keeps small text far more legible than nearest-neighbour.
	for y := range nh {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		for x := range nw {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			var r, g, bl, a, n uint32
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r, g, bl, a, n = r+cr>>8, g+cg>>8, bl+cb>>8, a+ca>>8, n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}

	var out bytes.Buffer
	// PNG, not JPEG: compression artifacts around letters hurt OCR.
	if err := png.Encode(&out, dst); err != nil {
		return data
	}
	return out.Bytes()
}
