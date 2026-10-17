package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// exifAPP1 builds an APP1 EXIF segment with Orientation and a GPS IFD pointer
// followed by a recognisable GPS payload.
func exifAPP1(orientation int) []byte {
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 2, 0}
	entry := func(tag, typ uint16, count, value uint32) []byte {
		b := make([]byte, 12)
		binary.LittleEndian.PutUint16(b, tag)
		binary.LittleEndian.PutUint16(b[2:], typ)
		binary.LittleEndian.PutUint32(b[4:], count)
		binary.LittleEndian.PutUint32(b[8:], value)
		return b
	}
	tiff = append(tiff, entry(0x0112, 3, 1, uint32(orientation))...)
	tiff = append(tiff, entry(0x8825, 4, 1, 38)...) // GPS IFD offset
	tiff = append(tiff, 0, 0, 0, 0)
	tiff = append(tiff, []byte("GPS 25.2048N 55.2708E serial-XYZ")...)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func segment(marker byte, payload string) []byte {
	seg := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// phoneJPEG is a w×h JPEG carrying EXIF (GPS), XMP, IPTC, a comment and an
// ICC profile, as phone cameras and editors write them.
func phoneJPEG(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{0, 0, 255, 255} // blue on the left half
			if x >= w/2 {
				c = color.RGBA{255, 0, 0, 255} // red on the right half
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	var out []byte
	out = append(out, raw[:2]...)
	out = append(out, exifAPP1(orientation)...)
	out = append(out, segment(0xE1, "http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>Creator: Jane Doe</x:xmpmeta>")...)
	out = append(out, segment(0xED, "Photoshop 3.0\x00IPTC by-line Jane Doe")...)
	out = append(out, segment(0xFE, "shot on device serial-XYZ")...)
	out = append(out, segment(0xE2, "ICC_PROFILE\x00\x01\x01fake-icc")...)
	out = append(out, segment(0xE2, "MPF\x00multi-picture index")...)
	out = append(out, raw[2:]...)
	// A trailing gain-map image with its own GPS, as HDR phone photos carry.
	return append(out, append(append([]byte{0xFF, 0xD8}, exifAPP1(1)...), 0xFF, 0xD9)...)
}

func TestStripJPEGRemovesMetadataLosslessly(t *testing.T) {
	data := phoneJPEG(t, 40, 20, 1)
	out, o, err := stripMetadata(data, "image/jpeg")
	if err != nil || o != 1 {
		t.Fatalf("strip: o=%d err=%v", o, err)
	}
	for _, leak := range []string{"GPS", "serial-XYZ", "Jane Doe", "Exif\x00\x00", "xmpmeta"} {
		if bytes.Contains(out, []byte(leak)) {
			t.Errorf("%q survived", leak)
		}
	}
	if !bytes.Contains(out, []byte("ICC_PROFILE\x00")) {
		t.Error("ICC profile dropped")
	}
	sos := func(b []byte) []byte {
		s := b[bytes.Index(b, []byte{0xFF, 0xDA}):]
		return s[:bytes.Index(s, []byte{0xFF, 0xD9})+2]
	}
	if !bytes.Equal(sos(out), sos(data)) || !bytes.HasSuffix(out, []byte{0xFF, 0xD9}) || bytes.Contains(out, []byte("MPF\x00")) {
		t.Error("entropy-coded data changed: stripping must not re-encode")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped JPEG does not decode: %v", err)
	}
}

func TestStripJPEGKeepsOnlyOrientation(t *testing.T) {
	out, o, err := stripMetadata(phoneJPEG(t, 40, 20, 6), "image/jpeg")
	if err != nil || o != 6 {
		t.Fatalf("o=%d err=%v", o, err)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Fatal("GPS survived")
	}
	i := bytes.Index(out, []byte("Exif\x00\x00"))
	if i < 0 || exifOrientation(out[i+6:]) != 6 {
		t.Fatal("orientation tag not kept")
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatal(err)
	}
}

func TestStripRejectsMalformed(t *testing.T) {
	for _, c := range []struct{ mime, data string }{{"image/jpeg", "\xFF\xD8\xFF\xE1\xFF\xFF"}, {"image/png", "\x89PNG\r\n\x1a\n\x00\x00"}, {"image/webp", "RIFF\x00\x00\x00\x00WEBPVP8X\xff\xff\xff\xff"}} {
		if _, _, err := stripMetadata([]byte(c.data), c.mime); err == nil {
			t.Errorf("%s: malformed input accepted", c.mime)
		}
	}
}

func pngChunk(typ string, payload []byte) []byte {
	b := make([]byte, 8, 12+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(payload)))
	copy(b[4:], typ)
	b = append(b, payload...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(append([]byte(typ), payload...)))
}

func TestStripPNGDropsTextChunks(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	ihdrEnd := 8 + 12 + 13
	data := append(append([]byte{}, raw[:ihdrEnd]...), pngChunk("tEXt", []byte("Author\x00Jane Doe"))...)
	data = append(data, pngChunk("eXIf", []byte("MM\x00\x2aGPS"))...)
	data = append(data, raw[ihdrEnd:]...)
	out, _, err := stripMetadata(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("Jane Doe")) || bytes.Contains(out, []byte("eXIf")) || !bytes.Equal(out, raw) {
		t.Fatal("PNG text/eXIf chunks not removed exactly")
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatal(err)
	}
}

func riffChunk(typ string, payload []byte) []byte {
	b := []byte(typ)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(payload)))
	b = append(b, payload...)
	if len(payload)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func TestStripWebPDropsExifAndXMP(t *testing.T) {
	vp8x := []byte{0x0C, 0, 0, 0, 7, 0, 0, 7, 0, 0} // EXIF+XMP flags, 8×8
	var body []byte
	body = append(body, riffChunk("VP8X", vp8x)...)
	body = append(body, riffChunk("VP8L", []byte{0x2f, 1, 2, 3, 4})...)
	body = append(body, riffChunk("EXIF", []byte("GPS 25N"))...)
	body = append(body, riffChunk("XMP ", []byte("<x>Jane Doe</x>"))...)
	data := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(4+len(body)))...)
	data = append(append(data, "WEBP"...), body...)
	out, _, err := stripMetadata(data, "image/webp")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) || bytes.Contains(out, []byte("Jane Doe")) {
		t.Fatal("WebP metadata survived")
	}
	if out[20]&0x0C != 0 {
		t.Fatalf("VP8X flags not cleared: %#x", out[20])
	}
	if int(binary.LittleEndian.Uint32(out[4:])) != len(out)-8 || !bytes.Contains(out, []byte("VP8L")) {
		t.Fatal("RIFF size or image chunk wrong")
	}
}

func TestUploadStripsMetadataAndOrientsVariants(t *testing.T) {
	s, _ := testService(t, false)
	ctx := context.Background()
	m, err := s.Upload(ctx, "phone.jpg", bytes.NewReader(phoneJPEG(t, 800, 400, 6)), UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := s.storage.Open(ctx, m.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	stored := mustRead(t, rc)
	if bytes.Contains(stored, []byte("GPS")) || bytes.Contains(stored, []byte("Jane Doe")) {
		t.Fatal("public original still carries metadata")
	}
	if m.Size != int64(len(stored)) {
		t.Fatalf("size %d, stored %d", m.Size, len(stored))
	}
	if m.Width != 400 || m.Height != 800 {
		t.Fatalf("dimensions should be as displayed (rotated 90°): %dx%d", m.Width, m.Height)
	}
	th, ok := m.Variants["thumbnail"]
	if !ok || th.Width != 150 || th.Height != 300 {
		t.Fatalf("thumbnail not upright: %+v", m.Variants)
	}
	rc, err = s.storage.Open(ctx, strings.TrimPrefix(th.URL, URLPrefix))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(mustRead(t, rc)))
	if err != nil {
		t.Fatal(err)
	}
	// Orientation 6 turns the photo 90° clockwise: blue (left) ends on top.
	top, _, b, _ := img.At(75, 20).RGBA()
	if b < top || b>>8 < 150 {
		t.Fatalf("thumbnail top is not blue: r=%d b=%d", top>>8, b>>8)
	}
}

func TestUploadKeepsMetadataWhenDisabled(t *testing.T) {
	s, _ := testService(t, false)
	ctx := context.Background()
	if err := s.settings.Set(ctx, SettingStripMetadata, false); err != nil {
		t.Fatal(err)
	}
	data := phoneJPEG(t, 40, 20, 1)
	m, err := s.Upload(ctx, "keep.jpg", bytes.NewReader(data), UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := s.storage.Open(ctx, m.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, rc), data) {
		t.Fatal("original changed although media.strip_metadata is false")
	}
}
