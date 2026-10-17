package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"
)

func TestVariantEncodingPreservesTransparencyAndGraphics(t *testing.T) {
	for _, transparent := range []bool{false, true} {
		img := image.NewRGBA(image.Rect(0, 0, 320, 160))
		alpha := uint8(255)
		if transparent {
			alpha = 128
		}
		for y := 0; y < 160; y++ {
			for x := 0; x < 320; x++ {
				img.Set(x, y, color.NRGBA{R: 80, G: 160, B: 220, A: alpha})
			}
		}
		v, err := encodeVariant(img, "image/png", "thumbnail")
		if err != nil {
			t.Fatal(err)
		}
		if v.Mime != "image/png" {
			t.Fatalf("graphic transparency=%v encoded as %s", transparent, v.Mime)
		}
		got, err := png.Decode(bytes.NewReader(v.Data))
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, a := got.At(10, 10).RGBA()
		if transparent && a == 65535 {
			t.Fatal("alpha lost")
		}
	}
}

func TestOpaquePhotoPNGUsesSmallerJPEGVariants(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	random := rand.New(rand.NewSource(1))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256)), 255})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	variants, err := makeVariants(original.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 {
		t.Fatalf("want medium and thumbnail, got %d", len(variants))
	}
	for _, v := range variants {
		if v.Mime != "image/jpeg" || v.Ext != "jpg" {
			t.Fatalf("photo not optimized: %+v", v.Name)
		}
		if len(v.Data) >= original.Len() {
			t.Fatal("variant larger than original")
		}
		t.Logf("%s: original=%d bytes variant=%d bytes", v.Name, original.Len(), len(v.Data))
		decoded, format, err := image.Decode(bytes.NewReader(v.Data))
		if err != nil {
			t.Fatal(err)
		}
		if format != "jpeg" || decoded.Bounds().Dx() != v.Width || decoded.Bounds().Dy() != v.Height {
			t.Fatal("wrong format or dimensions")
		}
	}
}

func TestVariantNeverCostsMoreThanOriginal(t *testing.T) {
	// A flat transparent PNG is already extremely compact. Any generated
	// variants must remain cheaper than that original, regardless of size.
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	variants, err := makeVariants(original.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range variants {
		if len(v.Data) >= original.Len() {
			t.Fatalf("%s wastes transfer bytes", v.Name)
		}
	}
}

func TestVariantsRejectOversizedHeaderBeforeDecode(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// Keep a valid IHDR checksum but dimensions far beyond the pixel budget.
	binary.BigEndian.PutUint32(data[16:20], 100000)
	binary.BigEndian.PutUint32(data[20:24], 100000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	_, err := makeVariants(data, "image/png")
	if err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("expected early pixel guard, got %v", err)
	}
}
