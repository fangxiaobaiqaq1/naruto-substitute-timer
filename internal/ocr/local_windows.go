//go:build windows && amd64 && cgo

package ocr

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"syscall"

	ort "github.com/yalue/onnxruntime_go"
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

func NewLocal(gpu ...bool) Recognizer {
	useGPU := len(gpu) > 0 && gpu[0]
	return &localRecognizer{newProcessRecognizer(func(ctx context.Context) *exec.Cmd {
		executable, err := os.Executable()
		cmd := exec.CommandContext(ctx, executable, helperArgument)
		cmd.Err = err
		provider := "cpu"
		if useGPU {
			provider = "directml"
		}
		cmd.Env = append(os.Environ(), "NARUTO_OCR_EXECUTION_PROVIDER="+provider)
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
	if os.Getenv("NARUTO_OCR_EXECUTION_PROVIDER") == "directml" {
		// DirectML is optional. The shipped CPU runtime or an older Windows
		// driver may reject the provider, so preserve recognition by falling
		// back to CPU instead of failing the OCR worker.
		_ = options.AppendExecutionProviderDirectML(0)
	}
	session, err := ort.NewDynamicAdvancedSessionWithONNXData(recognitionModel, []string{"x"}, []string{"softmax_11.tmp_0"}, options)
	if err != nil {
		return err
	}
	defer session.Destroy()
	characters, err := sessionCharacters(session)
	if err != nil {
		return err
	}
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
