package clipboard

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/clipcascade/pkg/constants"
)

func TestHandleChangeAllowsFallbackAfterDuplicateCandidate(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(30 * x), G: uint8(40 * y), B: 180, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	imagePayload := base64.StdEncoding.EncodeToString(buf.Bytes())

	m := NewManager()
	var gotType string
	var gotPayload string
	m.OnCopy(func(payload string, payloadType string, filename string) {
		gotType = payloadType
		gotPayload = payload
	})

	if !m.handleChange(imagePayload, constants.TypeImage, "") {
		t.Fatal("expected first image to be forwarded")
	}

	if m.handleChange(imagePayload, constants.TypeImage, "") {
		t.Fatal("expected duplicate image to be skipped")
	}

	textPayload := "second clipboard payload"
	if !m.handleChange(textPayload, constants.TypeText, "") {
		t.Fatal("expected fallback text payload to be forwarded")
	}

	if gotType != constants.TypeText || gotPayload != textPayload {
		t.Fatalf("unexpected forwarded payload: type=%q payload=%q", gotType, gotPayload)
	}
}
