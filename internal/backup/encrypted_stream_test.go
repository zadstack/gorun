package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"runtime"
	"strconv"
	"testing"
)

func TestLegacyEncryptedBackupFixtureStillAuthenticates(t *testing.T) {
	sealed, err := base64.StdEncoding.DecodeString("R09SVU4tQkFDS1VQLTEA9tY+HIwAeyIqgql2JKoUP4e/oK1C0YDbsqYP5eZc3prH9hlTfjdVy6/Tn08a9moMCg==")
	if err != nil {
		t.Fatal(err)
	}
	var plain bytes.Buffer
	if _, err = DecryptTo(&plain, bytes.NewReader(sealed), bytes.Repeat([]byte{42}, 32), "20261012T120000Z.db"); err != nil || plain.String() != "legacy backup fixture" {
		t.Fatalf("legacy recovery: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	plain.Reset()
	if _, err = DecryptTo(&plain, bytes.NewReader(sealed), bytes.Repeat([]byte{42}, 32), "20261012T120000Z.db"); err == nil || plain.Len() != 0 {
		t.Fatal("legacy unauthenticated output released")
	}
}

func TestStreamingBackupBoundariesAndUniqueFileKeys(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	name := "20261012T120000Z.db"
	for _, size := range []int{0, 1, 65536 - len(contentMarker), 65536, 65537, 2*65536 + 23} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			plain := bytes.Repeat([]byte{91}, size)
			var first, second, recovered bytes.Buffer
			if _, err := EncryptTo(&first, bytes.NewReader(plain), key, name); err != nil {
				t.Fatal(err)
			}
			if _, err := EncryptTo(&second, bytes.NewReader(plain), key, name); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatal("file key/nonce reused")
			}
			expectedSize, sizeErr := EncryptedSize(int64(size))
			if sizeErr != nil || expectedSize != int64(first.Len()) {
				t.Fatal("encrypted sizing mismatch", expectedSize, first.Len(), sizeErr)
			}
			n, err := DecryptTo(&recovered, bytes.NewReader(first.Bytes()), key, name)
			if err != nil || n != int64(size) || !bytes.Equal(recovered.Bytes(), plain) {
				t.Fatalf("roundtrip size=%d: %v", size, err)
			}
			if _, err = DecryptTo(io.Discard, bytes.NewReader(first.Bytes()), bytes.Repeat([]byte{1}, 32), name); err == nil {
				t.Fatal("wrong key accepted")
			}
			if _, err = DecryptTo(io.Discard, bytes.NewReader(first.Bytes()), key, "20261012T120001Z.db"); err == nil {
				t.Fatal("wrong filename accepted")
			}
		})
	}
}

func TestStreamingBackupRejectsDamagedOrReorderedStreams(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	name := "20261012T120000Z.db"
	var sealed bytes.Buffer
	if _, err := EncryptTo(&sealed, bytes.NewReader(bytes.Repeat([]byte{81}, 3*65536+3)), key, name); err != nil {
		t.Fatal(err)
	}
	original := sealed.Bytes()
	header := len(streamingMagic) + streamSaltSize
	packet := 65536 + 32
	reordered := bytes.Clone(original)
	copy(reordered[header:header+packet], original[header+packet:header+2*packet])
	copy(reordered[header+packet:header+2*packet], original[header:header+packet])
	tampered := bytes.Clone(original)
	tampered[len(tampered)-1] ^= 1
	salt := bytes.Clone(original)
	salt[len(streamingMagic)+4] ^= 1
	cases := map[string][]byte{"header": original[:len(streamingMagic)], "salt": salt, "truncated_tag": original[:len(original)-1], "missing_final_packet": original[:header+3*packet], "packet_boundary": original[:header+packet], "reordered": reordered, "tampered": tampered, "appended": append(bytes.Clone(original), 0)}
	for name, ciphertext := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecryptTo(io.Discard, bytes.NewReader(ciphertext), key, "20261012T120000Z.db"); err == nil {
				t.Fatal("damaged stream accepted")
			}
		})
	}
}

func TestStreamingBackup119MiBRoundTripUsesBoundedMemory(t *testing.T) {
	const size int64 = 119 << 20
	key := bytes.Repeat([]byte{42}, 32)
	name := "20261012T120000Z.tar.gz"
	reader, writer := io.Pipe()
	defer reader.Close()
	complete := make(chan error, 1)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	go func() {
		_, err := EncryptTo(writer, io.LimitReader(zeroBackupReader{}, size), key, name)
		writer.CloseWithError(err)
		complete <- err
	}()
	actual := sha256.New()
	n, err := DecryptTo(actual, reader, key, name)
	reader.Close()
	if encryptErr := <-complete; encryptErr != nil {
		t.Fatal(encryptErr)
	}
	runtime.ReadMemStats(&after)
	if err != nil || n != size {
		t.Fatalf("large recovery n=%d err=%v", n, err)
	}
	t.Logf("119MiB streaming roundtrip allocated %d bytes", after.TotalAlloc-before.TotalAlloc)
	if allocation := after.TotalAlloc - before.TotalAlloc; allocation > 8<<20 {
		t.Fatalf("streaming allocated %d bytes for119MiB", allocation)
	}
	expected := sha256.New()
	io.Copy(expected, io.LimitReader(zeroBackupReader{}, size))
	if !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
		t.Fatal("large file content changed")
	}
}
