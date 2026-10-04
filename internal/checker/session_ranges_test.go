package checker

import "testing"

func TestRangeHas(t *testing.T) {
	ranges := []LineRange{{From: 1, To: 10}, {From: 20, To: 20}, {From: 30, To: 45}}
	cases := []struct {
		n    int
		want bool
	}{
		{0, false}, {1, true}, {5, true}, {10, true}, {11, false},
		{19, false}, {20, true}, {21, false}, {29, false}, {30, true},
		{45, true}, {46, false},
	}
	for _, c := range cases {
		if got := RangeHas(ranges, c.n); got != c.want {
			t.Errorf("RangeHas(%d) = %v, want %v", c.n, got, c.want)
		}
	}
	if RangeHas(nil, 1) {
		t.Errorf("RangeHas(nil, 1) = true, want false")
	}
}

func TestAddDoneRangeMerges(t *testing.T) {
	var r []LineRange
	// Ascending: everything collapses into one range.
	for n := 1; n <= 50; n++ {
		r = addDoneRange(r, n)
	}
	if len(r) != 1 || r[0].From != 1 || r[0].To != 50 {
		t.Fatalf("ascending insert produced %v, want [{1 50}]", r)
	}
	// Duplicates and out-of-order inserts keep it sorted and disjoint. 57-59
	// are never marked, so the runs must stay separate.
	for _, n := range []int{60, 55, 56, 52, 51, 53, 54, 70, 69} {
		r = addDoneRange(r, n)
	}
	want := []LineRange{{From: 1, To: 56}, {From: 60, To: 60}, {From: 69, To: 70}}
	if len(r) != len(want) {
		t.Fatalf("got %v, want %v", r, want)
	}
	for i := range want {
		if r[i] != want[i] {
			t.Fatalf("got %v, want %v", r, want)
		}
	}
	// Re-adding existing points is a no-op.
	before := len(r)
	r = addDoneRange(r, 30)
	r = addDoneRange(r, 70)
	if len(r) != before {
		t.Fatalf("duplicate insert changed ranges: %v", r)
	}
	// Filling the remaining gaps bridges everything into one range.
	for _, n := range []int{57, 58, 59, 61, 62, 63, 64, 65, 66, 67, 68} {
		r = addDoneRange(r, n)
	}
	if len(r) != 1 || r[0].From != 1 || r[0].To != 70 {
		t.Fatalf("bridging failed: %v", r)
	}
	// Sorted order is preserved for the binary search in RangeHas.
	for i := 1; i < len(r); i++ {
		if r[i-1].To >= r[i].From {
			t.Fatalf("ranges not disjoint/sorted: %v", r)
		}
	}
	// n <= 0 is ignored.
	if got := addDoneRange(r, 0); len(got) != len(r) {
		t.Fatalf("n=0 mutated ranges: %v", got)
	}
}
