package launcher

import "testing"

func TestReusedParentPIDDoesNotClaimOldChildren(t *testing.T) {
	// Observed in real update acceptance: an old unrelated process still named
	// the new Node process's reused PID as its parent.
	parents := map[int]int{10: 1, 11: 10, 12: 11, 20: 10, 21: 20, 30: 11}
	created := map[int]uint64{10: 100, 11: 110, 12: 120, 20: 50, 21: 60, 30: 0}
	got := descendantsByCreation(10, parents, created)
	if len(got) != 3 || got[10] != 100 || got[11] != 110 || got[12] != 120 {
		t.Fatalf("wrong ancestry: %v", got)
	}
}
