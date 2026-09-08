package domain

import "time"

type Side string

const (
	Left  Side = "left"
	Right Side = "right"
)

type BeadState string

const (
	BeadUnknown BeadState = "unknown"
	BeadDark    BeadState = "dark"
	BeadLight   BeadState = "light"
	BeadGone    BeadState = "gone"
)

type ScreenState string

const (
	ScreenUnknown  ScreenState = "unknown"
	ScreenFighting ScreenState = "fighting"
	ScreenOther    ScreenState = "other"
	ScreenBlank    ScreenState = "blank"
)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type BeadScores struct {
	Light   float64 `json:"light"`
	Dark    float64 `json:"dark"`
	Gone    float64 `json:"gone"`
	Invalid float64 `json:"invalid"`
}

type BeadObservation struct {
	Index      int        `json:"index"`
	Center     Point      `json:"center"`
	State      BeadState  `json:"state"`
	Confidence float64    `json:"confidence"`
	Coverage   float64    `json:"coverage"`
	Scores     BeadScores `json:"scores"`
}

type SideObservation struct {
	Side       Side              `json:"side"`
	Raw        []BeadObservation `json:"raw"`
	Stable     []BeadState       `json:"stable"`
	Count      *int              `json:"count,omitempty"`
	Confidence float64           `json:"confidence"`
	Stale      bool              `json:"stale"`
}

type ScreenObservation struct {
	State      ScreenState `json:"state"`
	Confidence float64     `json:"confidence"`
}

type LayoutObservation struct {
	Confidence float64 `json:"confidence"`
	Relocated  bool    `json:"relocated"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Snapshot struct {
	Sequence       uint64            `json:"sequence"`
	CapturedAt     time.Time         `json:"capturedAt"`
	Screen         ScreenObservation `json:"screen"`
	Left           SideObservation   `json:"left"`
	Right          SideObservation   `json:"right"`
	Layout         LayoutObservation `json:"layout"`
	CaptureLatency time.Duration     `json:"captureLatency"`
	Diagnostics    []Diagnostic      `json:"diagnostics,omitempty"`
}
