package media

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // registers the GIF decoder
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder (decode only)
)

// maxPixels caps the decoded size of an image (decompression-bomb guard):
// larger images are stored as-is without variants.
const maxPixels = 40_000_000

const jpegQuality = 82

// Keep lossless graphics unless JPEG saves at least 20% of their bytes.
const jpegSavingsPercent = 20

// variantSpec is a named max-edge size. Variants are never upscaled.
type variantSpec struct {
	Name string
	Edge int
}

// variantSpecs are generated for raster images (spec §3 media).
var variantSpecs = []variantSpec{{"thumbnail", 300}, {"medium", 768}, {"large", 1536}}

// rasterMimes are the formats variants are generated for.
var rasterMimes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// imageConfig reads the dimensions without decoding pixels.
func imageConfig(data []byte) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	return cfg, err
}

// fitEdge scales w×h so its longest edge is edge, keeping aspect ratio.
func fitEdge(w, h, edge int) (int, int) {
	if w >= h {
		return edge, max(1, (h*edge+w/2)/w)
	}
	return max(1, (w*edge+h/2)/h), edge
}

// encodedVariant is one generated variant ready to store.
type encodedVariant struct {
	Name          string
	Data          []byte
	Ext, Mime     string
	Width, Height int
}

// variantSlots bounds concurrent decode+resize work (CPU and memory heavy)
// across all uploads.
var variantSlots = make(chan struct{}, 2)

// makeVariants decodes data (the first frame for animated GIFs) and returns
// the variants smaller than the original, resized with Catmull-Rom in a
// chain (large from the original, medium from large, thumbnail from medium)
// to cut CPU and memory. Transparent images remain lossless PNG. Opaque
// non-JPEG images use JPEG only when it is materially smaller than PNG.
// Originals are never modified; variants must be cheaper to transfer.
func makeVariants(data []byte, mimeType string) ([]encodedVariant, error) {
	cfg, err := imageConfig(data)
	if err != nil {
		return nil, fmt.Errorf("media: decode image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPixels/cfg.Height {
		return nil, fmt.Errorf("media: image exceeds pixel limit")
	}
	if max(cfg.Width, cfg.Height) <= variantSpecs[0].Edge {
		return nil, nil // avoid decoding images that cannot produce a variant
	}
	variantSlots <- struct{}{}
	defer func() { <-variantSlots }()
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("media: decode image: %w", err)
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	var out []encodedVariant
	var from image.Image = src
	for i := len(variantSpecs) - 1; i >= 0; i-- { // largest first
		spec := variantSpecs[i]
		if max(w, h) <= spec.Edge {
			continue // never upscale
		}
		vw, vh := fitEdge(w, h, spec.Edge)
		dst := image.NewRGBA(image.Rect(0, 0, vw, vh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), from, from.Bounds(), draw.Src, nil)
		from = dst
		v, err := encodeVariant(dst, mimeType, spec.Name)
		if err != nil {
			return nil, fmt.Errorf("media: encode %s: %w", spec.Name, err)
		}
		if len(v.Data) >= len(data) {
			continue // smaller dimensions alone do not justify a larger download
		}
		out = append(out, v)
	}
	return out, nil
}

// encodeVariant compares actual encoded bytes rather than assuming that a
// photograph is JPEG or a graphic is PNG based on its uploaded extension.
func encodeVariant(img *image.RGBA, sourceMIME, name string) (encodedVariant, error) {
	v := encodedVariant{Name: name, Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
	var lossless, lossy bytes.Buffer
	if sourceMIME != "image/jpeg" {
		if err := png.Encode(&lossless, img); err != nil {
			return v, err
		}
		v.Data, v.Ext, v.Mime = lossless.Bytes(), "png", "image/png"
		if !img.Opaque() {
			return v, nil
		}
	}
	if err := jpeg.Encode(&lossy, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return v, err
	}
	if sourceMIME == "image/jpeg" || lossy.Len()*100 <= lossless.Len()*(100-jpegSavingsPercent) {
		v.Data, v.Ext, v.Mime = lossy.Bytes(), "jpg", "image/jpeg"
	}
	return v, nil
}
