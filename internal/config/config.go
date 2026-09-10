package config

import (
	"os"

	"narutotimer/internal/domain"
)

const SchemaVersion = 1

type Config struct {
	SchemaVersion int            `json:"schemaVersion"`
	Device        DeviceConfig   `json:"device"`
	Capture       CaptureConfig  `json:"capture"`
	Layout        LayoutConfig   `json:"layout"`
	Vision        VisionConfig   `json:"vision"`
	Scene         SceneConfig    `json:"scene"`
	Sequence      SequenceConfig `json:"sequence"`
	Tracking      TrackingConfig `json:"tracking"`
	Debug         DebugConfig    `json:"debug"`
	UI            UIConfig       `json:"ui"`
}

// SceneConfig 是场景模板门闩的策略。模板清单在 manifest，这里只写表决规则。
type SceneConfig struct {
	Enabled        bool     `json:"enabled"`
	Manifest       string   `json:"manifest"`
	FightScenes    []string `json:"fightScenes"`
	EndScenes      []string `json:"endScenes"`
	HoldScenes     []string `json:"holdScenes"`
	MinFightScore  float64  `json:"minFightScore"`
	MinOtherScore  float64  `json:"minOtherScore"`
	Margin         float64  `json:"margin"`
	UncertainBelow float64  `json:"uncertainBelow"`
}

type DeviceConfig struct {
	ProcessNames     []string `json:"processNames"`
	TitlePatterns    []string `json:"titlePatterns"`
	SelectionPolicy  string   `json:"selectionPolicy"`
	RestoreMinimized bool     `json:"restoreMinimized"`
}

type CaptureConfig struct {
	MuMu             MuMuCaptureConfig `json:"mumu"`
	PreferredMethods []string          `json:"preferredMethods"`
	TimeoutMS        int               `json:"timeoutMs"`
	HUDRegion        NormalizedRect    `json:"hudRegion"`
}

type MuMuCaptureConfig struct {
	Selection  string `json:"selection,omitempty"`
	InstallDir string `json:"installDir"`
	DLLPath    string `json:"dllPath"`
	Instance   int    `json:"instance"`
	DisplayID  int    `json:"displayId"`
	Package    string `json:"package"`
}

type NormalizedRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type NormalizedPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type SideLayout struct {
	Search         NormalizedRect    `json:"search"`
	NominalCenters []NormalizedPoint `json:"nominalCenters"`
}

type LayoutConfig struct {
	PreferredProfile                   string                   `json:"preferredProfile"`
	Profiles                           map[string]LayoutProfile `json:"profiles,omitempty"`
	ReferenceWidth                     int                      `json:"referenceWidth"`
	ReferenceHeight                    int                      `json:"referenceHeight"`
	ContentMode                        string                   `json:"contentMode"`
	AutoAspectTolerance                float64                  `json:"autoAspectTolerance"`
	BeadsPerSide                       int                      `json:"beadsPerSide"`
	Left                               SideLayout               `json:"left"`
	Right                              SideLayout               `json:"right"`
	SearchRadiusX                      float64                  `json:"searchRadiusX"`
	SearchRadiusY                      float64                  `json:"searchRadiusY"`
	SpacingTolerance                   float64                  `json:"spacingTolerance"`
	MirrorTolerance                    float64                  `json:"mirrorTolerance"`
	RelocalizeAfterLowConfidenceFrames int                      `json:"relocalizeAfterLowConfidenceFrames"`
}

// LayoutProfile keeps camp and duel calibration independent. Coordinates are
// normalized to the game content area, before window scaling or letterboxing.
type LayoutProfile struct {
	Left  SideLayout `json:"left"`
	Right SideLayout `json:"right"`
}

// Profile resolves legacy left/right as camp when an explicit camp is absent.
// The built-in duel profile has four verified slots; six require explicit data.
func (l LayoutConfig) Profile(name string) LayoutProfile {
	if p, ok := l.Profiles[name]; ok {
		return p
	}
	if name == "duel" {
		return DefaultDuelProfile()
	}
	return LayoutProfile{Left: l.Left, Right: l.Right}
}

func DefaultDuelProfile() LayoutProfile {
	points := func(xs ...float64) []NormalizedPoint {
		out := make([]NormalizedPoint, len(xs))
		for i, x := range xs {
			out[i] = NormalizedPoint{X: x / 1600, Y: 103.0 / 900}
		}
		return out
	}
	return LayoutProfile{
		Left:  SideLayout{Search: NormalizedRect{X: 0.04, Y: 0.07, Width: 0.18, Height: 0.09}, NominalCenters: points(172, 198, 223, 249)},
		Right: SideLayout{Search: NormalizedRect{X: 0.78, Y: 0.07, Width: 0.18, Height: 0.09}, NominalCenters: points(1423, 1398, 1373, 1348)},
	}
}

type HSVRange struct {
	HMin float64 `json:"hMin"`
	HMax float64 `json:"hMax"`
	SMin float64 `json:"sMin"`
	SMax float64 `json:"sMax"`
	VMin float64 `json:"vMin"`
	VMax float64 `json:"vMax"`
}

type VisionConfig struct {
	Dark                    []HSVRange `json:"dark"`
	Light                   []HSVRange `json:"light"`
	Gold                    []HSVRange `json:"gold"`
	SampleWidthReferencePX  float64    `json:"sampleWidthReferencePx"`
	SampleHeightReferencePX float64    `json:"sampleHeightReferencePx"`
	CoreScale               float64    `json:"coreScale"`
	ColorWeight             float64    `json:"colorWeight"`
	ShapeWeight             float64    `json:"shapeWeight"`
	PositionWeight          float64    `json:"positionWeight"`
	SymmetryWeight          float64    `json:"symmetryWeight"`
	UnknownBelow            float64    `json:"unknownBelow"`
	MinimumMargin           float64    `json:"minimumMargin"`
	ScreenFightThreshold    float64    `json:"screenFightThreshold"`
}

type SequenceConfig struct {
	Allowed                  [][]domain.BeadState `json:"allowed"`
	UnknownPenalty           float64              `json:"unknownPenalty"`
	MinimumDecodedConfidence float64              `json:"minimumDecodedConfidence"`
}

type TrackingConfig struct {
	PollIntervalMS       int     `json:"pollIntervalMs"`
	WindowFrames         int     `json:"windowFrames"`
	MinimumConfirmFrames int     `json:"minimumConfirmFrames"`
	EnterFightFrames     int     `json:"enterFightFrames"`
	LeaveFightFrames     int     `json:"leaveFightFrames"`
	StateChangeMargin    float64 `json:"stateChangeMargin"`
	UnknownHoldMS        int     `json:"unknownHoldMs"`
	StaleAfterMS         int     `json:"staleAfterMs"`
	ResetAfterMS         int     `json:"resetAfterMs"`
}

type DebugConfig struct {
	Enabled             bool   `json:"enabled"`
	Directory           string `json:"directory"`
	SaveRaw             bool   `json:"saveRaw"`
	SaveAnnotated       bool   `json:"saveAnnotated"`
	SaveOnLowConfidence bool   `json:"saveOnLowConfidence"`
	MinimumIntervalMS   int    `json:"minimumIntervalMs"`
	RetentionFiles      int    `json:"retentionFiles"`
	LogFile             string `json:"logFile"`
}

// DebugOn 只认配置和环境变量，不猜。NARUTO_DEBUG=1 或 debug.enabled=true。
func (c Config) DebugOn() bool {
	if c.Debug.Enabled {
		return true
	}
	v := os.Getenv("NARUTO_DEBUG")
	return v == "1" || v == "true" || v == "TRUE"
}

type UIConfig struct {
	AutoTextRecognition       bool     `json:"autoTextRecognition"`
	PollIntervalMS            int      `json:"pollIntervalMs"`
	IdlePollIntervalMS        int      `json:"idlePollIntervalMs"`
	WindowWidth               int      `json:"windowWidth"`
	WindowHeight              int      `json:"windowHeight"`
	MiniWidth                 int      `json:"miniWidth"`
	MiniHeight                int      `json:"miniHeight"`
	SubstituteCooldownSeconds float64  `json:"substituteCooldownSeconds"` // Legacy setting retained for compatibility; current timer policy is 15s.
	SubstituteTable           string   `json:"substituteTable"`           // Historical data, not authoritative for live cooldown selection.
	NinjaQuery                string   `json:"ninjaQuery"`                // Full opponent variant override; empty means visual recognition.
	PlayerSide                string   `json:"playerSide"`
	RememberSide              bool     `json:"rememberSide"`
	AlwaysOnTop               bool     `json:"alwaysOnTop"`
	PlayerNames               []string `json:"playerNames"`
}

func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Device: DeviceConfig{
			ProcessNames:     []string{"MuMuNxDevice.exe", "MuMuPlayer.exe", "NemuPlayer.exe"},
			TitlePatterns:    []string{"MuMu"},
			SelectionPolicy:  "best-visible-client",
			RestoreMinimized: false,
		},
		Capture: CaptureConfig{
			MuMu:             MuMuCaptureConfig{Package: "com.tencent.KiHan"},
			PreferredMethods: []string{"mumu-sdk"},
			TimeoutMS:        1200,
			HUDRegion:        NormalizedRect{X: 0, Y: 0, Width: 1, Height: 0.24},
		},
		Layout: LayoutConfig{
			PreferredProfile: "auto",
			ReferenceWidth:   1920, ReferenceHeight: 1080, ContentMode: "auto",
			AutoAspectTolerance: 0.015, BeadsPerSide: 4,
			Left: SideLayout{
				Search:         NormalizedRect{X: 0.04, Y: 0.05, Width: 0.18, Height: 0.12},
				NominalCenters: []NormalizedPoint{{155.0 / 1600, 101.0 / 900}, {180.0 / 1600, 101.0 / 900}, {205.0 / 1600, 101.0 / 900}, {230.0 / 1600, 101.0 / 900}},
			},
			Right: SideLayout{
				Search:         NormalizedRect{X: 0.78, Y: 0.05, Width: 0.18, Height: 0.12},
				NominalCenters: []NormalizedPoint{{1391.0 / 1600, 101.0 / 900}, {1366.0 / 1600, 101.0 / 900}, {1340.0 / 1600, 101.0 / 900}, {1315.0 / 1600, 101.0 / 900}},
			},
			SearchRadiusX: 0.012, SearchRadiusY: 0.018,
			SpacingTolerance: 0.22, MirrorTolerance: 0.025,
			RelocalizeAfterLowConfidenceFrames: 3,
		},
		Vision: VisionConfig{
			// 空豆暗青实测 V≈0.35~0.40；亮豆 V≈0.85+。亮蓝下限必须高于空豆，否则第 4 颗会被算亮。
			Dark: []HSVRange{{HMin: 0.45, HMax: 0.80, SMin: 0.20, SMax: 1, VMin: 0.08, VMax: 0.52}},
			Light: []HSVRange{
				{HMin: 0.42, HMax: 0.68, SMin: 0.18, SMax: 1, VMin: 0.58, VMax: 1},
			},
			Gold: []HSVRange{
				{HMin: 0.00, HMax: 0.20, SMin: 0.25, SMax: 1, VMin: 0.45, VMax: 1},
				{HMin: 0.90, HMax: 1.00, SMin: 0.25, SMax: 1, VMin: 0.45, VMax: 1},
			},
			SampleWidthReferencePX: 13, SampleHeightReferencePX: 16, CoreScale: 0.55,
			ColorWeight: 0.55, ShapeWeight: 0.20, PositionWeight: 0.15, SymmetryWeight: 0.10,
			UnknownBelow: 0.35, MinimumMargin: 0.08, ScreenFightThreshold: 0.75,
		},
		Sequence: SequenceConfig{
			Allowed: [][]domain.BeadState{
				{domain.BeadDark, domain.BeadDark, domain.BeadDark, domain.BeadDark},
				{domain.BeadLight, domain.BeadDark, domain.BeadDark, domain.BeadDark},
				{domain.BeadLight, domain.BeadLight, domain.BeadDark, domain.BeadDark},
				{domain.BeadLight, domain.BeadLight, domain.BeadLight, domain.BeadDark},
				{domain.BeadLight, domain.BeadLight, domain.BeadLight, domain.BeadLight},
			},
			UnknownPenalty: 0.35, MinimumDecodedConfidence: 0.70,
		},
		Tracking: TrackingConfig{
			PollIntervalMS: 16, WindowFrames: 7, MinimumConfirmFrames: 2,
			EnterFightFrames: 1, LeaveFightFrames: 10, StateChangeMargin: 0.16,
			UnknownHoldMS: 1200, StaleAfterMS: 3000, ResetAfterMS: 8000,
		},
		Scene: SceneConfig{
			Enabled:        true,
			Manifest:       "assets/templates/manifest.json",
			FightScenes:    []string{"fight"},
			EndScenes:      []string{"lobby", "result"},
			HoldScenes:     []string{"vs", "queue", "pick", "ban"},
			MinFightScore:  0.62,
			MinOtherScore:  0.80,
			Margin:         0.05,
			UncertainBelow: 0.55,
		},
		Debug: DebugConfig{Directory: "debug", SaveOnLowConfidence: true, MinimumIntervalMS: 5000, RetentionFiles: 100},
		UI: UIConfig{
			PollIntervalMS: 16, IdlePollIntervalMS: 400,
			WindowWidth: 420, WindowHeight: 320,
			MiniWidth: 280, MiniHeight: 96,
			SubstituteCooldownSeconds: 15,
			SubstituteTable:           "assets/game/substitutes.json",
			PlayerSide:                "auto",
			AutoTextRecognition:       true,
			RememberSide:              true,
			AlwaysOnTop:               true,
			// Keep the published default neutral. Users can add their own names
			// in config.json or through the settings window.
			PlayerNames: []string{},
		},
	}
}
