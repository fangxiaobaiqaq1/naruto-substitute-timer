package leidian

import "testing"

func TestParseList2KeepsHWNDFields(t *testing.T) {
	items, err := parseList2([]byte("0,主号,1312034,656290,1,1234,5678,1280,720,240\r\n1,训练,0,0,0,-1,-1,1920,1080,280\r\n2,坏,abc,-5\r\n"), `E:\leidian\LDPlayer14`)
	if err != nil || len(items) != 3 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].TopHWND != 1312034 || items[0].BindHWND != 656290 || !items[0].Running || items[0].PID != 1234 || !items[0].AndroidStarted || items[0].Resolution != "1280x720" {
		t.Fatalf("unexpected running instance: %+v", items[0])
	}
	if items[1].TopHWND != 0 || items[1].BindHWND != 0 || items[1].Running || items[1].Serial != "127.0.0.1:5556" {
		t.Fatalf("unexpected stopped instance: %+v", items[1])
	}
	if items[2].TopHWND != 0 || items[2].BindHWND != 0 {
		t.Fatalf("malformed HWNDs should be zero: %+v", items[2])
	}
}
