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

func TestParseInstancesKeepsSeparateRootsWithSameZeroIndex(t *testing.T) {
	data := []byte(`{"0":{"index":"0","name":"主号","is_process_started":true},"2":{"index":2,"name":"小号","is_android_started":true}}`)
	left, err := parseInstances(data, `D:\MuMu`)
	if err != nil || len(left) != 2 {
		t.Fatal(left, err)
	}
	right, err := parseInstances(data, `E:\MuMu`)
	if err != nil || len(right) != 2 {
		t.Fatal(right, err)
	}
	if left[0].Root == right[0].Root || left[0].Index != 0 || right[0].Index != 0 {
		t.Fatalf("instance 0 roots were not distinct: %+v / %+v", left[0], right[0])
	}
	if !left[0].ProcessStarted || left[0].AndroidStarted || !left[1].AndroidStarted {
		t.Fatalf("start states lost: %+v", left)
	}
}
func TestParseInstancesPreservesManagerPIDAndStartStates(t *testing.T) {
	data := []byte(`{"0":{"index":0,"name":"主号","is_process_started":true,"pid":4321}}`)
	got, err := parseInstances(data, `C:\MuMu`)
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if got[0].PID != 4321 || !got[0].ProcessStarted || !got[0].Running {
		t.Fatalf("manager PID/start state lost: %+v", got[0])
	}
}
