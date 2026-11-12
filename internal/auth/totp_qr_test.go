package auth

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"rsc.io/qr"
)

var (
	qrViewBoxRe = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)
	qrPathRe    = regexp.MustCompile(` d="([^"]*)"`)
	qrRunRe     = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)
)

// svgModules parses the SVG back into a module grid (without quiet zone).
func svgModules(t *testing.T, svg string) [][]bool {
	t.Helper()
	vb := qrViewBoxRe.FindStringSubmatch(svg)
	path := qrPathRe.FindStringSubmatch(svg)
	if vb == nil || path == nil || vb[1] != vb[2] {
		t.Fatalf("unexpected SVG: %s", svg)
	}
	total, _ := strconv.Atoi(vb[1])
	n := total - 2*qrQuietZone
	grid := make([][]bool, n)
	for i := range grid {
		grid[i] = make([]bool, n)
	}
	rest := qrRunRe.ReplaceAllString(path[1], "")
	if rest != "" {
		t.Fatalf("path has unexpected commands: %q", rest)
	}
	for _, m := range qrRunRe.FindAllStringSubmatch(path[1], -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		w, _ := strconv.Atoi(m[3])
		if m[3] != m[4] || x < qrQuietZone || y < qrQuietZone || x+w > n+qrQuietZone || y >= n+qrQuietZone {
			t.Fatalf("run outside the code: %q", m[0])
		}
		for i := range w {
			grid[y-qrQuietZone][x-qrQuietZone+i] = true
		}
	}
	return grid
}

func TestQRSVGStructure(t *testing.T) {
	uri := totpURI([]byte("12345678901234567890"), `Site "<x>"`, "a&b@example.com")
	svg, err := qrSVG(uri, `Scan <me>`)
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	for _, want := range []string{`<svg xmlns="http://www.w3.org/2000/svg"`, `role="img"`, `aria-label="Scan &lt;me&gt;"`, `<rect `, `fill="#fff"`, `fill="#000"`} {
		if !strings.Contains(s, want) {
			t.Errorf("SVG lacks %s", want)
		}
	}
	// One element tree, no script, links, foreign content or data: URIs.
	for _, bad := range []string{"<script", "href", "data:", "<image", "foreignObject", "otpauth"} {
		if strings.Contains(s, bad) {
			t.Errorf("SVG contains %q", bad)
		}
	}
	code, _ := qr.Encode(uri, qr.M)
	grid := svgModules(t, s)
	if len(grid) != code.Size {
		t.Fatalf("size %d, encoder %d", len(grid), code.Size)
	}
	for y := range grid {
		for x := range grid[y] {
			if grid[y][x] != code.Black(x, y) {
				t.Fatalf("module (%d,%d) differs from the encoder's bitmap", x, y)
			}
		}
	}
}

// TestQRSVGDecodesToURI reads the drawn code back with an independent
// minimal decoder (ISO/IEC 18004: function patterns, zigzag placement,
// masks, block interleaving, byte mode; level M, versions 1–10, no error
// correction needed for a clean image).
func TestQRSVGDecodesToURI(t *testing.T) {
	for _, tc := range []struct{ issuer, account string }{
		{"", "a@example.com"},
		{"GoRun", "editor.with.a.long.name@example.org"},
		{"موقع", "ali@example.com"}, // escaped UTF-8 keeps it in versions 1–10
	} {
		uri := totpURI(newTOTPSecret(), tc.issuer, tc.account)
		svg, err := qrSVG(uri, "QR")
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeQR(svgModules(t, string(svg)))
		if err != nil {
			t.Fatalf("%q: %v", uri, err)
		}
		if got != uri {
			t.Fatalf("decoded %q, want %q", got, uri)
		}
	}
}

// Level M block structure (ISO/IEC 18004:2015 table 9): EC codewords per
// block and the data codewords of each block.
var qrLevelM = map[int]struct {
	ec   int
	data []int
}{
	1: {10, []int{16}}, 2: {16, []int{28}}, 3: {26, []int{44}}, 4: {18, []int{32, 32}},
	5: {24, []int{43, 43}}, 6: {16, []int{27, 27, 27, 27}}, 7: {18, []int{31, 31, 31, 31}},
	8: {22, []int{38, 38, 39, 39}}, 9: {22, []int{36, 36, 36, 37, 37}}, 10: {26, []int{43, 43, 43, 43, 44}},
}

// Alignment pattern centres (annex E).
var qrAlign = map[int][]int{
	2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34},
	7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50},
}

var qrMasks = [8]func(i, j int) bool{
	func(i, j int) bool { return (i+j)%2 == 0 },
	func(i, j int) bool { return i%2 == 0 },
	func(i, j int) bool { return j%3 == 0 },
	func(i, j int) bool { return (i+j)%3 == 0 },
	func(i, j int) bool { return (i/2+j/3)%2 == 0 },
	func(i, j int) bool { return (i*j)%2+(i*j)%3 == 0 },
	func(i, j int) bool { return ((i*j)%2+(i*j)%3)%2 == 0 },
	func(i, j int) bool { return ((i+j)%2+(i*j)%3)%2 == 0 },
}

func decodeQR(grid [][]bool) (string, error) {
	n := len(grid)
	v := (n - 17) / 4
	blocks, ok := qrLevelM[v]
	if !ok || n != 17+4*v {
		return "", fmt.Errorf("unsupported size %d", n)
	}
	fn := func(x, y int) bool {
		switch {
		case x < 9 && y < 9, x >= n-8 && y < 9, x < 9 && y >= n-8: // finders, separators, format info
			return true
		case x == 6 || y == 6: // timing
			return true
		case v >= 7 && ((x < 6 && y >= n-11 && y < n-8) || (y < 6 && x >= n-11 && x < n-8)): // version info
			return true
		}
		for _, cy := range qrAlign[v] {
			for _, cx := range qrAlign[v] {
				if (cx == 6 && cy == 6) || (cx == 6 && cy == n-7) || (cx == n-7 && cy == 6) {
					continue
				}
				if x >= cx-2 && x <= cx+2 && y >= cy-2 && y <= cy+2 {
					return true
				}
			}
		}
		return false
	}
	var lastErr error
	for _, mask := range qrMasks {
		// Zigzag: column pairs from the right, skipping the timing column.
		var bits []bool
		up := true
		for right := n - 1; right > 0; right -= 2 {
			if right == 6 {
				right = 5
			}
			for k := range n {
				y := k
				if up {
					y = n - 1 - k
				}
				for _, x := range []int{right, right - 1} {
					if !fn(x, y) {
						bits = append(bits, grid[y][x] != mask(y, x))
					}
				}
			}
			up = !up
		}
		stream := make([]byte, len(bits)/8)
		for i := range stream {
			for b := range 8 {
				if bits[i*8+b] {
					stream[i] |= 0x80 >> b
				}
			}
		}
		// De-interleave the data codewords.
		var data []byte
		maxLen := blocks.data[len(blocks.data)-1]
		perBlock := make([][]byte, len(blocks.data))
		pos := 0
		for i := range maxLen {
			for b, l := range blocks.data {
				if i < l {
					perBlock[b] = append(perBlock[b], stream[pos])
					pos++
				}
			}
		}
		for _, b := range perBlock {
			data = append(data, b...)
		}
		s, err := qrByteSegment(data, v)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// qrByteSegment reads one byte-mode segment followed by a terminator.
func qrByteSegment(data []byte, v int) (string, error) {
	pos := 0
	read := func(n int) int {
		out := 0
		for range n {
			bit := 0
			if pos/8 < len(data) && data[pos/8]&(0x80>>(pos%8)) != 0 {
				bit = 1
			}
			out = out<<1 | bit
			pos++
		}
		return out
	}
	if mode := read(4); mode != 0b0100 {
		return "", fmt.Errorf("mode %04b is not byte mode", mode)
	}
	countBits := 8
	if v >= 10 {
		countBits = 16
	}
	count := read(countBits)
	if (pos+count*8)/8 > len(data) {
		return "", fmt.Errorf("count %d exceeds the data", count)
	}
	out := make([]byte, count)
	for i := range out {
		out[i] = byte(read(8))
	}
	if term := read(4); term != 0 {
		return "", fmt.Errorf("no terminator")
	}
	return string(out), nil
}
