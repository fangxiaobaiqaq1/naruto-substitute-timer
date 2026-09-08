package app

import "testing"

func TestObservationSpaceRetainsKnownFieldsAcrossIncompleteFrames(t *testing.T) {
	var space ObservationSpace
	steps := []struct {
		name          string
		width, height int
		method        string
		changed       bool
	}{
		{"empty startup", 0, 0, "", false},
		{"initial source", 1600, 900, "mumu-sdk", false},
		{"same source", 1600, 900, "mumu-sdk", false},
		{"error with new geometry", 1280, 720, "mumu-sdk", true},
		{"error without metadata", 0, 0, "", false},
		{"partly missing dimensions", 1600, 0, "", false},
		{"recovery in new geometry", 1280, 720, "mumu-sdk", false},
		{"method changes without geometry", 0, 0, "printwindow", true},
		{"missing method", 1280, 720, "", false},
		{"same recovered method", 1280, 720, "printwindow", false},
		{"both change", 1920, 1080, "mumu-sdk", true},
	}
	for _, step := range steps {
		if got := space.Update(step.width, step.height, step.method); got != step.changed {
			t.Fatalf("%s: changed=%v, want %v", step.name, got, step.changed)
		}
	}
	if space.width != 1920 || space.height != 1080 || space.method != "mumu-sdk" {
		t.Fatalf("last known source lost: %+v", space)
	}
}

func TestObservationSpaceLearningMissingMetadataIsNotAChange(t *testing.T) {
	for _, dimensionsFirst := range []bool{false, true} {
		var space ObservationSpace
		if dimensionsFirst {
			space.Update(1600, 900, "")
		} else {
			space.Update(0, 0, "mumu-sdk")
		}
		if space.Update(1600, 900, "mumu-sdk") {
			t.Fatalf("learning an unknown field resynchronized the baseline, dimensionsFirst=%v", dimensionsFirst)
		}
	}
}
