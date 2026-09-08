package hudtext

import "testing"

func TestTableNamesSeparateCandidatesFromVerifiedVersions(t *testing.T) {
	d := NewDictionary("../../assets/game/substitutes.json")
	for _, tc := range []struct {
		raw  [variants]string
		want Title
	}{
		{[3]string{"奈良鹿丸(对面)", "奈良鹿丸(对面)", "鹿丸(对面)"}, Title{Ninja: "奈良鹿丸", Account: "对面"}},
		// Exact HUD spelling never allows a one-glyph OCR error to establish
		// identity; any approximate match remains a display-only candidate.
		{[3]string{"． 奈 良 鹿 九 （ 芍 鸯 黝", "奈 良 鹿 九 尚 鸯 鸟 于", "凵 奈 良 鹿 九 尚 鸯 鸟 于"}, Title{}},
		{[3]string{"漩涡呜人[暴怒·第六尾]", "漩涡呜人[暴怒·第六尾]", ""}, Title{Candidate: "漩涡鸣人"}},
		{[3]string{"照美具[五代目水影]", "照美具[五代目水影]", ""}, Title{}},
		{[3]string{"无名(奈良鹿丸)", "无名(奈良鹿丸)", ""}, Title{Account: "奈良鹿丸"}},
		{[3]string{"山中井", "山中井", ""}, Title{}},
		{[3]string{"一 神 秘 面 具 男 （ 仔 仔 ）", "一 神 秘 面 具 男 （ 仔 仔 ）", "凵 神 秘 面 具 男 （ 仔 仔 ）"}, Title{Account: "仔仔"}},
		{[3]string{"甲乙神秘面具男(对面)", "甲乙神秘面具男(对面)", ""}, Title{Account: "对面"}},
		{[3]string{"神秘面具男(对面)", "神秘面具男(对面)", ""}, Title{Ninja: "神秘面具男", Account: "对面"}},
		{[3]string{"猿飞木叶丸(对面)", "猿飞木叶丸(对面)", ""}, Title{Ninja: "猿飞木叶丸", Account: "对面"}},
		{[3]string{"旗木卡卡西(对面)", "旗木卡卡西(对面)", ""}, Title{Ninja: "旗木卡卡西", Account: "对面"}},
	} {
		if got := d.consensus(tc.raw); got != tc.want {
			t.Errorf("%q: %+v want %+v", tc.raw, got, tc.want)
		}
	}
	if got := (Dictionary{"甲乙丙丁": true, "甲乙丙戊": true}).candidate("甲乙丙己"); got != "" {
		t.Fatalf("ambiguous candidates: %s", got)
	}
	if got := d.consensus([3]string{"奈良鹿九"}); got != (Title{}) {
		t.Fatalf("single treatment published candidate: %+v", got)
	}
}

func TestCatalogSuffixCannotAuthorizeInventedHUDName(t *testing.T) {
	d := Dictionary{"甲乙": true, "丙丁戊己": true}
	for _, text := range []string{"噪甲乙", "噪声甲乙", "噪丙丁戊己", "噪声丙丁戊己"} {
		if d.knownBase(text) {
			t.Fatalf("invented prefix accepted: %s", text)
		}
	}
	if (Dictionary{}).knownBase("奈良鹿丸") {
		t.Fatal("HUD expansion without catalog base")
	}
}
