package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
)

// Uploaded originals are public, so camera metadata (GPS position, device
// serials, owner names, edit history) is removed before they are stored.
// Stripping is lossless: metadata segments are dropped and pixels are never
// re-encoded. Colour profiles stay, and a JPEG keeps a minimal EXIF block
// holding only its Orientation, so browsers still show it upright.
//
//   - JPEG: drops APP1 (EXIF, XMP), APP2 other than ICC (MPF), APP3–APP13
//     (IPTC, Photoshop…), APP15, COM and anything after the main image's EOI
//     (gain/depth maps); keeps APP0 (JFIF), APP2 ICC_PROFILE, APP14 (Adobe).
//   - PNG: drops eXIf, tEXt, zTXt, iTXt and tIME chunks.
//   - WebP: drops EXIF and XMP chunks and clears their VP8X flags.
//
// GIF has no camera metadata and is stored as uploaded.

// SettingStripMetadata turns stripping off when set to false (default on).
const SettingStripMetadata = "media.strip_metadata"

var errBadImage = errors.New("media: malformed image container")

func (s *service) stripEnabled(ctx context.Context) bool {
	var on bool
	if err := s.settings.Get(ctx, SettingStripMetadata, &on); err != nil {
		return true
	}
	return on
}

// stripMetadata returns data without metadata and the EXIF orientation (1–8,
// 1 when absent). Unknown formats are returned unchanged.
func stripMetadata(data []byte, mimeType string) ([]byte, int, error) {
	switch mimeType {
	case "image/jpeg":
		return stripJPEG(data)
	case "image/png":
		out, err := stripPNG(data)
		return out, 1, err
	case "image/webp":
		out, err := stripWebP(data)
		return out, 1, err
	}
	return data, 1, nil
}

func stripJPEG(data []byte) ([]byte, int, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, 1, errBadImage
	}
	orientation := 1
	var kept [][]byte
	i := 2
	for {
		if i+4 > len(data) || data[i] != 0xFF {
			return nil, 1, errBadImage
		}
		marker := data[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xD9 || marker == 0xDA { // EOI, or SOS: entropy-coded data follows verbatim
			break
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) { // standalone markers
			kept = append(kept, data[i:i+2])
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return nil, 1, errBadImage
		}
		seg, payload := data[i:i+2+n], data[i+4:i+2+n]
		switch {
		case marker == 0xE1:
			if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) && orientation == 1 {
				orientation = exifOrientation(payload[6:])
			}
		case marker == 0xE2 && !bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00")):
		case marker >= 0xE3 && marker <= 0xED, marker == 0xEF, marker == 0xFE:
		default:
			kept = append(kept, seg)
		}
		i += 2 + n
	}
	var out bytes.Buffer
	out.Grow(len(data))
	out.Write(data[:2])
	if orientation != 1 {
		// After JFIF APP0 when present, else right after SOI.
		if len(kept) > 0 && len(kept[0]) > 1 && kept[0][1] == 0xE0 {
			out.Write(kept[0])
			kept = kept[1:]
		}
		out.Write(orientationAPP1(orientation))
	}
	for _, seg := range kept {
		out.Write(seg)
	}
	// Entropy-coded data never contains FF D9 (FF is stuffed as FF 00), so
	// the first EOI ends the main image. Trailers after it (MPF gain or
	// depth maps, editor blobs) are dropped with their own metadata.
	rest := data[i:]
	if eoi := bytes.Index(rest, []byte{0xFF, 0xD9}); eoi >= 0 {
		rest = rest[:eoi+2]
	}
	out.Write(rest)
	return out.Bytes(), orientation, nil
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF header; 1 if absent.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(tiff[4:]))
	if off < 8 || off+2 > len(tiff) {
		return 1
	}
	count := int(bo.Uint16(tiff[off:]))
	for k := 0; k < count; k++ {
		e := off + 2 + 12*k
		if e+12 > len(tiff) {
			return 1
		}
		if bo.Uint16(tiff[e:]) == 0x0112 && bo.Uint16(tiff[e+2:]) == 3 {
			if v := int(bo.Uint16(tiff[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orientationAPP1 is an EXIF block holding only the Orientation tag.
func orientationAPP1(o int) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, byte(o), 0, 0, 0, 0, 0, 0}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

var pngDrop = map[string]bool{"eXIf": true, "tEXt": true, "zTXt": true, "iTXt": true, "tIME": true}

func stripPNG(data []byte) ([]byte, error) {
	const sig = "\x89PNG\r\n\x1a\n"
	if len(data) < len(sig) || string(data[:len(sig)]) != sig {
		return nil, errBadImage
	}
	var out bytes.Buffer
	out.Grow(len(data))
	out.WriteString(sig)
	for i := len(sig); i < len(data); {
		if i+12 > len(data) {
			return nil, errBadImage
		}
		n := int(binary.BigEndian.Uint32(data[i:]))
		end := i + 12 + n
		if n < 0 || end > len(data) || end < i {
			return nil, errBadImage
		}
		typ := string(data[i+4 : i+8])
		if !pngDrop[typ] {
			out.Write(data[i:end])
		}
		i = end
		if typ == "IEND" {
			break
		}
	}
	return out.Bytes(), nil
}

func stripWebP(data []byte) ([]byte, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, errBadImage
	}
	var body bytes.Buffer
	vp8x := -1
	for i := 12; i < len(data); {
		if i+8 > len(data) {
			return nil, errBadImage
		}
		n := int(binary.LittleEndian.Uint32(data[i+4:]))
		end := i + 8 + n + n%2 // chunks are padded to an even size
		if n < 0 || end > len(data) || end < i {
			if i+8+n == len(data) { // tolerate a missing final pad byte
				end = len(data)
			} else {
				return nil, errBadImage
			}
		}
		switch string(data[i : i+4]) {
		case "EXIF", "XMP ":
		case "VP8X":
			vp8x = body.Len()
			body.Write(data[i:end])
		default:
			body.Write(data[i:end])
		}
		i = end
	}
	b := body.Bytes()
	if vp8x >= 0 && vp8x+8 < len(b) {
		b[vp8x+8] &^= 0x08 | 0x04 // EXIF and XMP present flags
	}
	out := make([]byte, 12, 12+len(b))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(4+len(b)))
	copy(out[8:], "WEBP")
	return append(out, b...), nil
}

// orient returns img as displayed for an EXIF orientation (2–8).
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
