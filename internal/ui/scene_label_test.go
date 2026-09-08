package ui

import (
	"narutotimer/internal/config"
	"strings"
	"testing"
)

func TestRecognizedSceneNamesRemainVisibleWhileHolding(t *testing.T) {
	for id, want := range map[string]string{"vs": "VS", "queue": "匹配", "pick": "选人", "ban": "禁用忍者", "fight": "决斗场", "unsupported-resolution": "画面比例未校准"} {
		s := &session{cfg: config.Default(), scene: id, hold: true}
		line := s.statusLine()
		if !strings.Contains(line, want) || strings.Contains(line, "画面未识别") {
			t.Fatalf("%s lost its label: %s", id, line)
		}
	}
}

func TestRememberedFightContextIsNotReportedAsNewSceneRecognition(t *testing.T) {
	for profile, want := range map[string]string{"camp": "训练场", "duel": "决斗场"} {
		s := &session{cfg: config.Default(), scene: "fight", layoutProfile: profile, fighting: true, hold: true}
		line := s.statusLine()
		if !strings.Contains(line, want+" · 画面遮挡") || strings.Contains(line, "待识别") {
			t.Fatalf("retained scene mislabeled as a new/unknown scene: %s", line)
		}
		if s.visibleReady(true) != "?" || s.visibleReady(false) != "?" {
			t.Fatal("remembered scene fabricated current beans")
		}
	}
}
