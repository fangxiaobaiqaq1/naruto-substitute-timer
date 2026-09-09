package hudtext

import "testing"

func TestBundledMiddleDotNamesRemainExact(t *testing.T) {
	d := NewDictionary("")
	for _, name := range []string{"萨克·镫", "金·槌", "佩恩·天道", "梅塔尔·李"} {
		if got := d.consensus([variants]string{name + "(账号)", name + "(账号)"}); got != (Title{Ninja: name, Account: "账号"}) {
			t.Errorf("embedded name %q: %+v", name, got)
		}
	}
	for _, raw := range []string{"萨克镫", "萨克・镫", "萨克.镫", "萨克·灯", "噪萨克·镫", "萨克·镫噪"} {
		if got := d.consensus([variants]string{raw, raw}); got.Ninja != "" {
			t.Errorf("non-exact name %q established identity: %+v", raw, got)
		}
	}
}

func TestCatalogMiddleDotNamesNeedNoHardcodedAlias(t *testing.T) {
	d := Dictionary{}
	d.addCatalog([]byte(`{"ninjas":[{"name":"甲乙・丙丁"}],"substitutes":[{"name":"替身术_戊己·庚辛"}]}`))
	for _, name := range []string{"甲乙・丙丁", "戊己·庚辛"} {
		if got := d.parse(name); got.Ninja != name {
			t.Errorf("data-only name %q: %+v", name, got)
		}
	}
	for _, raw := range []string{"甲乙·丙丁", "甲乙丙丁", "戊己・庚辛", "戊己庚辛"} {
		if got := d.parse(raw); got.Ninja != "" {
			t.Errorf("punctuation alias %q established identity: %+v", raw, got)
		}
	}
	for _, raw := range []string{"·甲乙", "甲乙·", "甲··乙", "甲·・乙", "甲.乙", "甲/乙", "甲1乙", "甲", "甲乙丙丁戊己庚辛壬癸子"} {
		d.addName(raw)
		if d[raw] {
			t.Errorf("malformed catalog base accepted: %q", raw)
		}
	}
}
