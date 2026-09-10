//go:build windows && amd64

package mumu

import "testing"

func TestParseInstancesKeepsNonzeroIDsNamesAndStoppedEntries(t *testing.T) {
	data := []byte(`{"3":{"index":"3","name":"玩家主号","is_process_started":true},"7":{"index":7,"name":"训练营","is_android_started":true},"0":{"index":"0","name":"未开"},"bad":{"name":"bad"}}`)
	got, e := parseInstances(data, "C:/MuMu")
	if e != nil || len(got) != 3 {
		t.Fatal(got, e)
	}
	seen := map[int]Instance{}
	for _, i := range got {
		seen[i.Index] = i
	}
	if !seen[3].Running || !seen[7].Running || seen[0].Running || seen[3].Name != "玩家主号" {
		t.Fatal(seen)
	}
}
