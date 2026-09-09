// Package factory 提供引擎工厂：按配置构造检测引擎（默认 RGB）。
// 独立子包避免 import cycle（engine 接口包不依赖任何引擎实现）。
// 预留模型引擎接入点：配置 Kind = "model" 时切换实现，UI/CLI 零改动。
package factory

import (
	"image"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/rgb"
	"narutotimer/internal/hudtext"
	"narutotimer/internal/identity"
	"narutotimer/internal/ocr"
	"narutotimer/internal/scene"
)

// Kind 是引擎类型标识。
type Kind string

const (
	KindRGB    Kind = "rgb"    // 纯 RGB 区间判色
	KindOpenCV Kind = "opencv" // OpenCV HSV + 菱形邻域（-tags opencv）
	KindModel  Kind = "model"  // 模型推理（预留，未实现）
)

// Config 是引擎配置。App 为加载后的 JSON 配置，禁止在实现里再 Default()。
type Config struct {
	Kind Kind
	Mode detect.ContentMode
	App  config.Config
}

// DefaultConfig 返回默认配置。带 -tags opencv 时默认走 OpenCV。
func DefaultConfig() Config {
	return FromApp(config.Default())
}

// FromApp 用已加载的应用配置构造引擎工厂配置。
func FromApp(appCfg config.Config) Config {
	mode, err := detect.ParseMode(appCfg.Layout.ContentMode)
	if err != nil {
		mode = detect.ModeAuto
	}
	return Config{Kind: defaultKind, Mode: mode, App: appCfg}
}

// New 按配置构造引擎。
func New(cfg Config) (engine.Engine, error) {
	inner, err := newInner(cfg)
	if err != nil {
		return nil, err
	}
	gate, _, err := scene.NewGate(cfg.App)
	if err != nil {
		return nil, err
	}
	return engine.NewGatedWithGuess(gate, inner, adaptGuesser(cfg.App)), nil
}

func adaptGuesser(cfg config.Config) engine.SideGuesser {
	g := identity.Guesser(cfg)
	if g == nil {
		return nil
	}
	return func(img *image.RGBA, scene string) engine.Identity {
		r := g(img, scene)
		return engine.Identity{Side: r.Side, Mine: r.Mine, Opp: r.Opp}
	}
}

func newInner(cfg Config) (engine.Engine, error) {
	switch cfg.Kind {
	case "", KindRGB:
		return rgb.NewConfigured(engine.NewConfiguredLayout(cfg.Mode, cfg.App.Layout), cfg.App.Vision), nil
	case KindOpenCV:
		return newOpenCV(cfg)
	case KindModel:
		return nil, &UnsupportedError{Kind: string(cfg.Kind)}
	default:
		return nil, &UnsupportedError{Kind: string(cfg.Kind)}
	}
}

// UnsupportedError 表示引擎类型未实现/不支持。
type UnsupportedError struct{ Kind string }

func (e *UnsupportedError) Error() string {
	return "引擎类型暂不支持: " + e.Kind + "（当前支持 rgb；opencv 需 -tags opencv）"
}

// MustDefault 构造默认 RGB 引擎（panic on error，供入口便捷使用）。
func MustDefault() engine.Engine {
	eng, err := New(DefaultConfig())
	if err != nil {
		panic(err)
	}
	return eng
}

// NewLive enables bounded background OCR for interactive capture. Offline New
// intentionally stays deterministic and does not spawn system helpers. The live
// caller must Close the wrapper at shutdown, including capture setup failures.
func NewLive(cfg Config) (*hudtext.Engine, error) {
	inner, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return hudtext.New(inner, cfg.App, ocr.NewLocal(), identity.MineNames), nil
}
