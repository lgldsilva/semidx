package eval

import "testing"

func TestEvaluateUsesGradedNDCGAndDeduplicatedRecall(t *testing.T) {
	q := Query{ID: "q", Query: "auth", Relevant: []Relevance{
		{File: "auth.go", Grade: 3},
		{File: "middleware.go", Grade: 2},
	}}
	ranked := []Ranked{{File: "noise.go"}, {File: "auth.go"}, {File: "auth.go"}, {File: "middleware.go"}}
	m := Evaluate(q, ranked, 4)
	if m.MRR != 0.5 {
		t.Fatalf("MRR = %v, want 0.5", m.MRR)
	}
	if m.Recall10 != 1 {
		t.Fatalf("Recall10 = %v, want 1", m.Recall10)
	}
	if m.NDCG10 <= 0 || m.NDCG10 >= 1 {
		t.Fatalf("NDCG10 = %v, want a partial graded score", m.NDCG10)
	}
}

func TestMetricsBoundaries(t *testing.T) {
	q := Query{ID: "q", Query: "x", Relevant: []Relevance{{File: "a.go", Grade: 3}}}
	if got := NDCG(nil, Grades(q), 10); got != 0 {
		t.Fatalf("empty NDCG = %v", got)
	}
	if got := MRR(nil, Grades(q)); got != 0 {
		t.Fatalf("empty MRR = %v", got)
	}
	if got := PrecisionAt(nil, Grades(q), 5); got != 0 {
		t.Fatalf("empty precision = %v", got)
	}
	if got := RecallAt(nil, Grades(q), 5); got != 0 {
		t.Fatalf("empty recall = %v", got)
	}
	if got := NDCG([]Ranked{{File: "a.go"}}, Grades(q), 10); got != 1 {
		t.Fatalf("perfect NDCG = %v, want 1", got)
	}
	if got := Evaluate(q, []Ranked{{File: "a.go"}}, 0); got.NDCG10 != 1 {
		t.Fatalf("Evaluate default top-k = %+v", got)
	}
	if got := PrecisionAt([]Ranked{{File: "a.go"}}, Grades(q), 0); got != 0 {
		t.Fatalf("zero-k precision = %v", got)
	}
}

func TestPrecisionUsesPositionsAndRecallUsesUniqueFiles(t *testing.T) {
	grades := map[string]int{"a.go": 1, "b.go": 1}
	ranked := []Ranked{{File: "a.go"}, {File: "a.go"}, {File: "noise.go"}, {File: "b.go"}}
	if got := PrecisionAt(ranked, grades, 2); got != 0.5 {
		t.Fatalf("precision = %v, want 0.5 (duplicate consumes rank but is not a second hit)", got)
	}
	if got := RecallAt(ranked, grades, 4); got != 1 {
		t.Fatalf("recall = %v, want 1", got)
	}
}

func TestNDCGBoundsAndPromotionMonotonicity(t *testing.T) {
	grades := map[string]int{"relevant.go": 3}
	for rank := 0; rank < 20; rank++ {
		results := make([]Ranked, 20)
		for i := range results {
			results[i] = Ranked{File: "noise-" + string(rune('a'+i)) + ".go"}
		}
		results[rank] = Ranked{File: "relevant.go"}
		score := NDCG(results, grades, 20)
		if score < 0 || score > 1 {
			t.Fatalf("rank %d produced nDCG %v outside 0..1", rank+1, score)
		}
		if rank > 0 {
			promoted := append([]Ranked(nil), results...)
			promoted[rank], promoted[rank-1] = promoted[rank-1], promoted[rank]
			if got := NDCG(promoted, grades, 20); got < score {
				t.Fatalf("promoting relevant from rank %d reduced nDCG: %v < %v", rank+1, got, score)
			}
		}
	}
}

func TestRecallCutoffExcludesRelevantAtRankEleven(t *testing.T) {
	results := make([]Ranked, 11)
	for i := range results {
		results[i] = Ranked{File: "noise.go"}
	}
	results[10] = Ranked{File: "relevant.go"}
	if got := RecallAt(results, map[string]int{"relevant.go": 3}, 10); got != 0 {
		t.Fatalf("Recall@10 = %v, want 0 for relevant result at rank 11", got)
	}
}
