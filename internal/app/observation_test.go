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
		{"failed capture of another method", 0, 0, "printwindow", false},
		{"missing method", 1280, 720, "", false},
		{"first image of the new method", 1280, 720, "printwindow", true},
		{"same method", 1280, 720, "printwindow", false},
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

// GE-09: failed captures carry the method id ("mumu-sdk") while frames with an
// image carry the client's Source() ("MuMu 实例 0 · …"). Only a frame with an
// image describes the coordinate system, so an error between two good frames
// is not a source change, neither on the way in nor on the way back.
func TestObservationSpaceIgnoresMethodOfFramesWithoutImage(t *testing.T) {
	var space ObservationSpace
	source := "MuMu 实例 0 · C:\\MuMu"
	for i, step := range []struct {
		width, height int
		method        string
	}{{1920, 1080, source}, {0, 0, "mumu-sdk"}, {1920, 1080, source}, {0, 0, "mumu-sdk"}, {0, 0, ""}, {1920, 1080, source}} {
		if space.Update(step.width, step.height, step.method) {
			t.Fatalf("step %d (%dx%d %q) reported a source change", i, step.width, step.height, step.method)
		}
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
