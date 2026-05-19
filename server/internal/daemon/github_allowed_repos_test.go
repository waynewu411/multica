package daemon

import "testing"

func TestAllowedRepoSet_EmptyMeansNoRestriction(t *testing.T) {
	if got := allowedRepoSet(nil); got != nil {
		t.Errorf("allowedRepoSet(nil) = %v, want nil (no restriction)", got)
	}
	if got := allowedRepoSet([]string{}); got != nil {
		t.Errorf("allowedRepoSet([]) = %v, want nil (no restriction)", got)
	}
}

func TestAllowedRepoSet_BuildsLookupSet(t *testing.T) {
	got := allowedRepoSet([]string{"owner/a", "owner/b"})
	if got == nil {
		t.Fatal("got nil, want a non-nil set")
	}
	if _, ok := got["owner/a"]; !ok {
		t.Error("owner/a missing from set")
	}
	if _, ok := got["owner/b"]; !ok {
		t.Error("owner/b missing from set")
	}
	if _, ok := got["owner/c"]; ok {
		t.Error("owner/c should not be in set")
	}
}
