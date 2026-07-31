package localstore

import "testing"

func TestPercentileMS(t *testing.T) {
	t.Parallel()
	if got := percentileMS(nil, 0.5); got != 0 {
		t.Fatalf("empty=%v", got)
	}
	sample := []int64{10, 20, 30, 40, 50}
	if got := percentileMS(sample, 0.5); got != 30 {
		t.Fatalf("p50=%v want 30", got)
	}
	if got := percentileMS(sample, 0.95); got != 50 {
		t.Fatalf("p95=%v want 50", got)
	}
	if got := percentileMS([]int64{7}, 0.95); got != 7 {
		t.Fatalf("single=%v", got)
	}
}
