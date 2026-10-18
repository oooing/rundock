package delivery

import "testing"

func TestCombinedSyncState(t *testing.T) {
	for _, tc := range []struct {
		states []string
		want   string
	}{
		{[]string{"verified", "verified"}, "verified"},
		{[]string{"verified", "pending"}, "pending"},
		{[]string{"unverified", "verified"}, "unverified"},
		{[]string{"unconfigured", "verified"}, "unconfigured"},
	} {
		if got := combinedSyncState(tc.states); got != tc.want {
			t.Fatalf("%v: %s", tc.states, got)
		}
	}
}
