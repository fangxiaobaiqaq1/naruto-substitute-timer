//go:build !windows || !amd64 || !cgo

package ocr

func NewLocal() Recognizer { return NewSystem() }

func BackendName() string { return "系统 OCR" }
