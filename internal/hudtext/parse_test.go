package hudtext

import "testing"

func TestConsensusDoesNotGuessAccountsOrVersions(t *testing.T) {
	d := NewDictionary("")
	for _, tc := range []struct {
		raw  [3]string
		want Title
	}{
		{[3]string{"油女志乃(蚌埠住啦!)", "油 女 志 乃（蚌埠住啦!）", "岫女志乃(蚌埠住啦!)"}, Title{Ninja: "油女志乃"}},
		{[3]string{"油女态乃", "岫女志乃", "岫女乃"}, Title{Candidate: "油女志乃"}},
		{[3]string{"山 中 井 野 （ 白 方 小 ）", "山中井野(白方小)", "山中井野[噪声]"}, Title{Ninja: "山中井野", Account: "白方小"}},
		{[3]string{"照美冥[五代目水影](对手)", "照美冥【五代目水影】（对手）", "照美具[五代目水影](别名)"}, Title{Ninja: "照美冥[五代目水影]", Account: "对手"}},
		{[3]string{"照美冥[五代目水影](对手)", "照美冥[泳装](对手)", "照美冥(对手)"}, Title{Ninja: "照美冥", Account: "对手"}},
		{[3]string{"小南[漂泊浪客](白方小)", "小南[漂泊氵良客](自方小)", "小南(白方小号)"}, Title{Ninja: "小南"}},
		{[3]string{"春野檯(白方小)", "春野檯(白方小)", "春野槐(自方小)"}, Title{Account: "白方小"}},
		{[3]string{"纲手[少女]{吓死本帅帅了）", "纲手[少女](吓死本帅帅了)", "纲手[少女]"}, Title{Ninja: "纲手[少女]", Account: "吓死本帅帅了"}},
		{[3]string{"(白方小额外)", "(白方小)", "(自方小)"}, Title{}},
	} {
		if got := d.consensus(tc.raw); got != tc.want {
			t.Errorf("%q: got %+v want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestSingleFrameSingleReadingNeverEstablishesIdentity(t *testing.T) {
	d := NewDictionary("")
	if got := d.consensus([3]string{"照美冥[五代目水影](白方小)"}); got != (Title{}) {
		t.Fatalf("unconfirmed OCR accepted: %+v", got)
	}
	for _, raw := range []string{"白方小", "abc(白方小)noise", "abc(白方小)(other)", "abc(白 方 小);Invoke-Evil", "abc(白/方小)"} {
		if p := d.parse(raw); p.Account == "白方小" {
			t.Fatalf("malformed account accepted %q", raw)
		}
	}
}
