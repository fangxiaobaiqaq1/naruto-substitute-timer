//go:build (windows && amd64 && cgo) || ocrhost

package ocr

// Row recognition shared by the Windows OCR helper process (local_windows.go)
// and the offline host harness (local_host.go, tag ocrhost): the same row
// preprocessing, PP-OCRv4 session call and CTC decoding on every platform.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"strings"

	ort "github.com/yalue/onnxruntime_go"
	xdraw "golang.org/x/image/draw"
)

// sessionCharacters is the CTC alphabet: blank, the model's "character"
// metadata (one per line), then the space the PP-OCR export appends.
func sessionCharacters(session *ort.DynamicAdvancedSession) ([]string, error) {
	metadata, err := session.GetModelMetadata()
	if err != nil {
		return nil, err
	}
	dictionary, present, err := metadata.LookupCustomMetadataMap("character")
	metadata.Destroy()
	if err != nil || !present {
		return nil, fmt.Errorf("OCR model character dictionary unavailable: %v", err)
	}
	characters := append([]string{""}, strings.Split(strings.TrimRight(dictionary, "\r\n"), "\n")...)
	return append(characters, " "), nil
}

func recognizeRequest(session *ort.DynamicAdvancedSession, characters []string, encoded string, regions []image.Rectangle) ([]Line, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) > 3<<20 {
		return nil, fmt.Errorf("invalid OCR image")
	}
	info, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || info.Width < 1 || info.Height < 1 || info.Width > 2400 || info.Height > 2400 {
		return nil, fmt.Errorf("invalid OCR dimensions")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(regions) == 0 {
		regions = []image.Rectangle{img.Bounds()}
	}
	if len(regions) > 8 {
		return nil, fmt.Errorf("too many OCR rows")
	}
	var lines []Line
	for _, region := range regions {
		if region.Empty() || !region.In(img.Bounds()) {
			return nil, fmt.Errorf("invalid OCR row")
		}
		line, err := recognizeRow(session, characters, img, region)
		if err != nil {
			return nil, err
		}
		if len(line.Words) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func recognizeRow(session *ort.DynamicAdvancedSession, characters []string, img image.Image, region image.Rectangle) (Line, error) {
	width := int(math.Ceil(float64(region.Dx()) * 48 / float64(region.Dy())))
	width = max(16, min(1600, width))
	resized := image.NewRGBA(image.Rect(0, 0, width, 48))
	xdraw.BiLinear.Scale(resized, resized.Bounds(), img, region, xdraw.Src, nil)
	plane := width * 48
	input := make([]float32, plane*3)
	for y := 0; y < 48; y++ {
		for x := 0; x < width; x++ {
			c := resized.RGBAAt(x, y)
			p := y*width + x
			input[p] = float32(c.B)/127.5 - 1
			input[plane+p] = float32(c.G)/127.5 - 1
			input[2*plane+p] = float32(c.R)/127.5 - 1
		}
	}
	tensor, err := ort.NewTensor(ort.NewShape(1, 3, 48, int64(width)), input)
	if err != nil {
		return Line{}, err
	}
	defer tensor.Destroy()
	outputs := []ort.Value{nil}
	if err := session.Run([]ort.Value{tensor}, outputs); err != nil {
		return Line{}, err
	}
	defer outputs[0].Destroy()
	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return Line{}, fmt.Errorf("invalid OCR output type")
	}
	shape := output.GetShape()
	if len(shape) != 3 || shape[0] != 1 || shape[2] != int64(len(characters)) {
		return Line{}, fmt.Errorf("invalid OCR output shape %v, dictionary %d", shape, len(characters))
	}
	return decodeCharacters(output.GetData(), int(shape[1]), characters, region), nil
}

func decodeCharacters(values []float32, steps int, characters []string, region image.Rectangle) Line {
	type token struct {
		index, step int
		score       float32
	}
	var tokens []token
	previous := -1
	for step := 0; step < steps; step++ {
		row := values[step*len(characters) : (step+1)*len(characters)]
		best := 0
		for i, p := range row {
			if p > row[best] {
				best = i
			}
		}
		if best != 0 && best != previous {
			tokens = append(tokens, token{best, step, row[best]})
		}
		previous = best
	}
	var line Line
	for i, t := range tokens {
		// Do not silently delete uncertain glyphs and turn a partial name into
		// another dictionary entry. The replacement marker prevents exact matches.
		text := strings.TrimSuffix(characters[t.index], "\r")
		if t.score < .45 {
			text = "�"
		}
		center := (float64(t.step) + .5) * float64(region.Dx()) / float64(steps)
		left, right := center-8*float64(region.Dy())/24, center+8*float64(region.Dy())/24
		if i > 0 {
			left = (float64(tokens[i-1].step+t.step) + 1) / 2 * float64(region.Dx()) / float64(steps)
		}
		if i+1 < len(tokens) {
			right = (float64(tokens[i+1].step+t.step) + 1) / 2 * float64(region.Dx()) / float64(steps)
		}
		left = max(0, left)
		right = min(float64(region.Dx()), right)
		line.Words = append(line.Words, Word{Text: text, X: float64(region.Min.X) + left, Y: float64(region.Min.Y), Width: right - left, Height: float64(region.Dy())})
		line.Text += text
	}
	return line
}
