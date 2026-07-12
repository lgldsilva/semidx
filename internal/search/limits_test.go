package search

import "testing"

func TestClampGraphDepth(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, DefaultGraphDepth},
		{-1, DefaultGraphDepth},
		{1, 1},
		{5, MaxGraphDepth},
		{99, MaxGraphDepth},
	}
	for _, tc := range tests {
		if got := ClampGraphDepth(tc.in); got != tc.want {
			t.Errorf("ClampGraphDepth(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
