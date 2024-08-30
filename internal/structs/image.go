package structs

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"

	"github.com/disintegration/imaging"
)

type Image []byte

func (i *Image) Resize(width, height int) (Image, error) {
	img, _, err := image.Decode(bytes.NewReader(*i))
	if err != nil {
		return nil, fmt.Errorf("error decoding image: %w", err)
	}

	resized := imaging.Resize(img, width, height, imaging.Lanczos)

	var buf bytes.Buffer
	err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85})
	if err != nil {
		return nil, fmt.Errorf("error encoding resized image: %w", err)
	}

	return buf.Bytes(), nil
}

func (i *Image) ToAnthropicContent() AnthropicContent {
	mediaType := http.DetectContentType(*i)

	base64Image := base64.StdEncoding.EncodeToString(*i)

	return AnthropicContent{
		Type: "image",
		Source: &AnthropicImageSource{
			Type:      "base64",
			MediaType: mediaType,
			Data:      base64Image,
		},
	}
}
