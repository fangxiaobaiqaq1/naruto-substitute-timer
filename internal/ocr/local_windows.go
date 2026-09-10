//go:build windows && amd64 && cgo

package ocr

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"strings"
	"syscall"

	ort "github.com/yalue/onnxruntime_go"
	xdraw "golang.org/x/image/draw"
)

//go:embed neuraldata/recognizer.onnx
var recognitionModel []byte

//go:embed neuraldata/onnxruntime.dll
var recognitionRuntime []byte

const helperArgument = "--timer-local-ocr-worker-v1"

// Re-executing this binary keeps the neural runtime outside the capture/UI
// process. Existing timeout/Close handling can terminate a stuck inference.
func init() {
	if len(os.Args) == 2 && os.Args[1] == helperArgument {
		if err := runLocalWorker(); err != nil {
			writeRuntimeFailure(err)
			_ = json.NewEncoder(os.Stdout).Encode(response{Error: runtimeErrorSummary(err)})
			os.Exit(1)
		}
		os.Exit(0)
	}
}

type localRecognizer struct{ *processRecognizer }

func (*localRecognizer) PrefersColorRows() bool { return true }

func NewLocal() Recognizer {
	return &localRecognizer{newProcessRecognizer(func(ctx context.Context) *exec.Cmd {
		executable, err := os.Executable()
		cmd := exec.CommandContext(ctx, executable, helperArgument)
		cmd.Err = err
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		return cmd
	})}
}

func BackendName() string { return "本机 PP-OCRv4（模型及运行依赖已内置）" }

func runLocalWorker() (err error) {
	dll, loaded, err := prepareLocalRuntime()
	if err != nil {
		return err
	}
	defer loaded.close()
	defer func() {
		if err != nil {
			err = &runtimeContextError{Err: err, Loaded: loaded.paths}
		}
	}()
	ort.SetSharedLibraryPath(dll)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("初始化 ONNX 环境失败: %w", err)
	}
	defer ort.DestroyEnvironment()
	options, err := ort.NewSessionOptions()
	if err != nil {
		return err
	}
	defer options.Destroy()
	if err := options.SetIntraOpNumThreads(2); err != nil {
		return err
	}
	if err := options.SetInterOpNumThreads(1); err != nil {
		return err
	}
	session, err := ort.NewDynamicAdvancedSessionWithONNXData(recognitionModel, []string{"x"}, []string{"softmax_11.tmp_0"}, options)
	if err != nil {
		return err
	}
	defer session.Destroy()
	metadata, err := session.GetModelMetadata()
	if err != nil {
		return err
	}
	dictionary, present, err := metadata.LookupCustomMetadataMap("character")
	metadata.Destroy()
	if err != nil || !present {
		return fmt.Errorf("OCR model character dictionary unavailable: %v", err)
	}
	characters := append([]string{""}, strings.Split(strings.TrimRight(dictionary, "\r\n"), "\n")...)
	characters = append(characters, " ")
	encoder := json.NewEncoder(os.Stdout)
	writeRuntimeStatus(loaded.paths)
	if err := encoder.Encode(response{Ready: true, RuntimeLibraries: loaded.paths}); err != nil {
		return err
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 4<<20)
	for scan.Scan() {
		var request struct {
			ID      uint64            `json:"id"`
			Image   string            `json:"image"`
			Regions []image.Rectangle `json:"regions"`
		}
		if err := json.Unmarshal(scan.Bytes(), &request); err != nil {
			return err
		}
		lines, err := recognizeRequest(session, characters, request.Image, request.Regions)
		out := response{ID: request.ID, Lines: lines}
		if err != nil {
			out.Error = err.Error()
		}
		if err := encoder.Encode(out); err != nil {
			return err
		}
	}
	return scan.Err()
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
