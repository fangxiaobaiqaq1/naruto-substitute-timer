//go:build !windows || !amd64 || !cgo

package ocr

func NewLocal(...bool) Recognizer { return NewSystem() }

func BackendName() string { return "系统 OCR" }
