package ocr

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The precomputed digests name the extraction directory and must describe the
// exact embedded files (checked on every platform, not only Windows).
func TestRuntimeDigestsMatchEmbeddedFiles(t *testing.T) {
	paths := map[string]string{"onnxruntime.dll": "neuraldata/onnxruntime.dll"}
	for _, name := range []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll", "msvcp140_1.dll"} {
		paths[name] = filepath.Join("neuraldata", "vcruntime", name)
	}
	if len(paths) != len(runtimeDigests) {
		t.Fatalf("digests for %d libraries, %d embedded", len(runtimeDigests), len(paths))
	}
	for name, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != runtimeDigests[name] {
			t.Fatalf("%s: sha256 %s, table %s", name, got, runtimeDigests[name])
		}
	}
}
