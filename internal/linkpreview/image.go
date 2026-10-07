package linkpreview

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/webp"
)

// Keep native PNG/JPEGs; convert GIF's first frame and WebP to supported photos.
// Check dimensions before decoding so small files cannot allocate huge images.
func photo(data []byte) ([]byte, string) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width+config.Height > 10000 ||
		max(config.Width, config.Height) > 20*min(config.Width, config.Height) {
		return nil, ""
	}
	if format == "png" || format == "jpeg" {
		return data, format
	}
	if config.Width*config.Height > 4_000_000 {
		return nil, ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ""
	}
	var output bytes.Buffer
	if opaque, ok := img.(interface{ Opaque() bool }); ok && opaque.Opaque() {
		format, err = "jpeg", jpeg.Encode(&output, img, &jpeg.Options{Quality: 85})
	} else {
		format, err = "png", png.Encode(&output, img)
	}
	if err != nil || output.Len() > maxImage {
		return nil, ""
	}
	return output.Bytes(), format
}
