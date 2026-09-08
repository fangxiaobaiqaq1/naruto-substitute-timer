package detect

import "testing"

func TestPossiblePrefixPreservesOnlyIndependentEvidence(t *testing.T) {
	for _, tc := range []struct {
		states   []BeadState
		possible bool
	}{
		{[]BeadState{StateLight, StateUnknown, StateLight, StateDark}, true},
		{[]BeadState{StateLight, StateUnknown, StateDark, StateDark}, true},
		{[]BeadState{StateUnknown, StateUnknown, StateUnknown, StateUnknown}, true},
		{[]BeadState{StateDark, StateUnknown, StateLight, StateDark}, false},
		{[]BeadState{StateLight, StateDark, StateLight, StateUnknown}, false},
	} {
		if got := IsPossiblePrefix(tc.states); got != tc.possible {
			t.Errorf("%v: possible=%v", tc.states, got)
		}
		if IsLegalPrefix(tc.states) {
			t.Errorf("partial/contradictory row supplied a legal count: %v", tc.states)
		}
	}
}
