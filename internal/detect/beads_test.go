package detect

import "testing"

func TestApplyIncrementRuleKeepsUnknown(t *testing.T) {
	in := []BeadState{StateLight, StateLight, StateLight, StateUnknown}
	got := ApplyIncrementRule(in)
	if got[3] != StateUnknown {
		t.Fatalf("unknown must not be rewritten to dark, got %v", got)
	}
	illegal := ApplyIncrementRule([]BeadState{StateDark, StateLight, StateLight, StateLight})
	for i, s := range illegal {
		if s != StateUnknown {
			t.Fatalf("illegal prefix must not become a ready count, [%d]=%s", i, s)
		}
	}
	clean := ApplyIncrementRule([]BeadState{StateLight, StateLight, StateLight, StateDark})
	if clean[0] != StateLight || clean[3] != StateDark {
		t.Fatalf("legal prefix should stay, got %v", clean)
	}
}

func TestClassifyGoldIsReady(t *testing.T) {
	if got := Classify(255, 238, 1); got != StateLight {
		t.Fatalf("gold bead must count as ready, got %s", got)
	}
	if got := Classify(249, 193, 22); got != StateLight {
		t.Fatalf("gold bead must count as ready, got %s", got)
	}
	if got := Classify(252, 82, 23); got != StateUnknown {
		t.Fatalf("HP bar red must not count as a bead, got %s", got)
	}
	if got := Classify(156, 42, 17); got != StateUnknown {
		t.Fatalf("explosion red must not count as gold, got %s", got)
	}
	if got := Classify(255, 255, 255); got != StateUnknown {
		t.Fatalf("blank white must not count as ready, got %s", got)
	}
	if got := Classify(112, 156, 120); got != StateLight {
		t.Fatalf("duel cyan bead must be light, got %s", got)
	}
	if got := Classify(183, 236, 244); got != StateLight {
		t.Fatalf("cyan bead must be light, got %s", got)
	}
	if got := Classify(20, 46, 90); got != StateDark {
		t.Fatalf("dark cyan must be dark, got %s", got)
	}
}

func TestComputeContentAreaStretch(t *testing.T) {
	// 16:9 客户区 → 铺满
	ca := ComputeContentArea(1920, 1080, ModeAuto)
	if ca.X != 0 || ca.Y != 0 || ca.W != 1920 || ca.H != 1080 {
		t.Fatalf("1920x1080 auto 应为铺满, got %+v", ca)
	}
	// 接近 16:9 → 也视为铺满
	ca = ComputeContentArea(1280, 720, ModeAuto)
	if ca.W != 1280 || ca.H != 720 {
		t.Fatalf("1280x720 auto 应为铺满, got %+v", ca)
	}
	// 强制拉伸
	ca = ComputeContentArea(860, 580, ModeStretch)
	if ca.W != 860 || ca.H != 580 {
		t.Fatalf("stretch 应铺满客户区, got %+v", ca)
	}
}

func TestComputeContentAreaLetterbox(t *testing.T) {
	// 860x580 (约 1.48:1) 且非 16:9 → 等比黑边
	// 缩放比 = min(860/1920, 580/1080) = min(0.4479, 0.5370) = 0.4479
	// 内容区 = 860 x (1080*0.4479≈483)，垂直居中
	ca := ComputeContentArea(860, 580, ModeAuto)
	wantW, wantH := 860, 483
	if ca.W != wantW || ca.H != wantH {
		t.Fatalf("等比内容区应 %dx%d, got %dx%d", wantW, wantH, ca.W, ca.H)
	}
	if ca.X != 0 || ca.Y != (580-wantH)/2 {
		t.Fatalf("等比偏移应 (0,%d), got (%d,%d)", (580-wantH)/2, ca.X, ca.Y)
	}
	// 强制等比
	ca = ComputeContentArea(1920, 1080, ModeLetterbox)
	if ca.W != 1920 || ca.H != 1080 {
		t.Fatalf("1920x1080 letterbox 内容区应全屏, got %+v", ca)
	}
	// 横向黑边场景：客户区比 16:9 更宽
	ca = ComputeContentArea(2000, 1000, ModeLetterbox)
	// scale = min(2000/1920, 1000/1080) = 1000/1080 ≈ 0.9259
	// 内容宽 = int(1920*1000/1080) = 1777，高 = 1000
	if ca.W != 1777 || ca.H != 1000 {
		t.Fatalf("横向黑边内容区应 1777x1000, got %dx%d", ca.W, ca.H)
	}
	if ca.X != (2000-1777)/2 || ca.Y != 0 {
		t.Fatalf("横向黑边偏移应 ((2000-1777)/2, 0), got (%d,%d)", ca.X, ca.Y)
	}
}

func TestMap(t *testing.T) {
	ca := ComputeContentArea(1920, 1080, ModeAuto)
	x, y := ca.Map(206, 110)
	if x != 206 || y != 110 {
		t.Fatalf("全屏映射应保持原值, got (%d,%d)", x, y)
	}
	// 860x483 内容区（垂直偏移 48），逻辑(206,110) → (92, 97)
	ca = ComputeContentArea(860, 580, ModeAuto)
	x, y = ca.Map(206, 110)
	if x != 92 || y != 97 {
		t.Fatalf("等比映射错误, got (%d,%d), want (92,97)", x, y)
	}
}

func TestLayoutCount(t *testing.T) {
	pos := Layout(1920, 1080, ModeAuto, DefaultBeads())
	if len(pos) != 8 {
		t.Fatalf("默认应有 8 个豆, got %d", len(pos))
	}
	var left, right int
	for _, p := range pos {
		if p.Side == "left" {
			left++
		} else {
			right++
		}
	}
	if left != 4 || right != 4 {
		t.Fatalf("左右默认各 4 豆, got left=%d right=%d", left, right)
	}
	if pos[1].X <= pos[0].X {
		t.Fatalf("左豆 x 应递增")
	}
	if pos[5].X >= pos[4].X {
		t.Fatalf("右豆 x 应递减")
	}
}

func TestSixBeadTrailingEmptyIsLegal(t *testing.T) {
	four := []BeadState{StateLight, StateLight, StateLight, StateLight, StateUnknown, StateUnknown}
	if !IsLegalPrefix(four) {
		t.Fatalf("4-bead HUD with empty 5/6 must stay legal")
	}
	got := ApplyIncrementRule(four)
	if got[0] != StateLight || got[3] != StateLight || got[4] != StateUnknown {
		t.Fatalf("trailing empty must stay empty, got %v", got)
	}
	six := []BeadState{StateLight, StateLight, StateLight, StateLight, StateLight, StateDark}
	if !IsLegalPrefix(six) {
		t.Fatalf("6-bead prefix LLLLLD must be legal")
	}
	if CountLight(six) != 5 {
		t.Fatalf("ready=5, got %d", CountLight(six))
	}
}

func TestDuelBeadsShiftRightOfCamp(t *testing.T) {
	camp := Layout(1920, 1080, ModeAuto, DefaultBeads())
	duel := Layout(1920, 1080, ModeAuto, DuelBeads())
	if len(duel) != 8 {
		t.Fatalf("duel beads = %d", len(duel))
	}
	if duel[0].X <= camp[0].X {
		t.Fatalf("duel left beads should sit right of camp, camp=%d duel=%d", camp[0].X, duel[0].X)
	}
}
