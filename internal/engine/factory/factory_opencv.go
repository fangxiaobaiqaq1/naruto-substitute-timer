//go:build opencv

package factory

import (
	"narutotimer/internal/engine"
	"narutotimer/internal/vision/opencv"
)

const defaultKind = KindOpenCV

func newOpenCV(cfg Config) (engine.Engine, error) {
	return opencv.New(engine.NewConfiguredLayout(cfg.Mode, cfg.App.Layout), cfg.App), nil
}
