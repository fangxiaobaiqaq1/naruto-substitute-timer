//go:build !opencv

package factory

import "narutotimer/internal/engine"

const defaultKind = KindRGB

func newOpenCV(cfg Config) (engine.Engine, error) {
	_ = cfg
	return nil, &UnsupportedError{Kind: string(KindOpenCV)}
}
