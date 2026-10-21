package ocr

// runtimeDigests are the SHA-256 digests of the embedded native runtime
// libraries (neuraldata/onnxruntime.dll and neuraldata/vcruntime/*.dll).
// Precomputed so the OCR helper does not hash ~19 MB of embedded DLLs on
// every launch; TestRuntimeDigestsMatchEmbeddedFiles keeps them in sync with
// the files (update both together).
var runtimeDigests = map[string]string{
	"onnxruntime.dll":    "e6de9dcd61a85c4100edafe286d3fd2639513210b81b9dd50d200c4199a06c01",
	"vcruntime140.dll":   "d1f4225df2cd877dbf130d5668a021dce3f94118455ff5ec952061c30afc9ce7",
	"vcruntime140_1.dll": "a7146c08f89fe5b04541ab507cdb59ff7b44534d4ba3c668a426c6450a03434e",
	"msvcp140.dll":       "7c26614e1d733892c2deac7e245ce115504b1d80592dd0a01b08e3e5a55f89ca",
	"msvcp140_1.dll":     "206c931bf90fdad8816de3b5e2ef80b2bcaa9406c89ecc05fe6fddffe251e982",
}
