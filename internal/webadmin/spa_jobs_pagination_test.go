package webadmin

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestPaginateJobs(t *testing.T) {
	jobs := []store.Job{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}

	tests := []struct {
		name          string
		limit, offset int
		wantIDs       []int
	}{
		{name: "first page", limit: 2, offset: 0, wantIDs: []int{1, 2}},
		{name: "middle page", limit: 2, offset: 1, wantIDs: []int{2, 3}},
		{name: "offset only", limit: 0, offset: 2, wantIDs: []int{3, 4}},
		{name: "overflow offset", limit: 2, offset: 9, wantIDs: []int{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := paginateJobs(jobs, tc.limit, tc.offset)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("len=%d want=%d", len(got), len(tc.wantIDs))
			}
			for i, w := range tc.wantIDs {
				if got[i].ID != w {
					t.Fatalf("job[%d].ID=%d want=%d", i, got[i].ID, w)
				}
			}
		})
	}
}
