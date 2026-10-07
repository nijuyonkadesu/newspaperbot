package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

func TestImageValidationRejectsWrongFormatTruncationAndOversizedPayloads(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	photo := data.Bytes()
	if err := ValidateImage(photo, "image.png"); err != nil {
		t.Fatal(err)
	}
	tooWide := bytes.Clone(photo)
	binary.BigEndian.PutUint32(tooWide[16:20], 5000)
	binary.BigEndian.PutUint32(tooWide[20:24], 5000)
	binary.BigEndian.PutUint32(tooWide[29:33], crc32.ChecksumIEEE(tooWide[12:29]))
	for _, example := range []struct {
		name string
		data []byte
		file string
	}{
		{"wrong extension", photo, "image.jpg"},
		{"truncated", photo[:len(photo)/2], "image.png"},
		{"not image", []byte("HTML from a failed download"), "image.png"},
		{"too many bytes", bytes.Repeat([]byte{'x'}, MaxImageBytes+1), "image.png"},
		{"too many pixels", tooWide, "image.png"},
	} {
		t.Run(example.name, func(t *testing.T) {
			if err := ValidateImage(example.data, example.file); err == nil {
				t.Fatal("invalid image accepted")
			}
		})
	}
}
