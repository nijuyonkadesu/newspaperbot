// Package media validates bounded image files before they enter the repository.
package media

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	_ "golang.org/x/image/webp"
)

const MaxImageBytes = 5 << 20

type ImageError struct{ FileID, Reason string }

func (e *ImageError) Error() string { return e.Reason }

func ValidateImage(data []byte, name string) error {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return errors.New("Image exceeds 5 MiB · send a smaller image")
	}
	c, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || c.Width <= 0 || c.Height <= 0 || int64(c.Width)*int64(c.Height) > 16000000 {
		return errors.New("Image invalid or exceeds 16 megapixels · resend a smaller image")
	}
	ext := format
	if format == "jpeg" {
		ext = "jpg"
	}
	if !strings.HasSuffix(name, "."+ext) {
		return errors.New("Image format does not match its attachment · resend it")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return errors.New("Image incomplete · resend it")
	}
	return nil
}
