package eval

import "math"

// Evaluate computes graded nDCG@10 and binary MRR/precision/recall metrics.
// Duplicate result files count once for relevance but still consume rank, which
// exposes a retriever that overproduces adjacent chunks from one file.
func Evaluate(q Query, ranked []Ranked, k int) QueryMetrics {
	if k <= 0 {
		k = 10
	}
	grades := Grades(q)
	m := QueryMetrics{ID: q.ID, Query: q.Query, Found: len(ranked), Relevant: len(grades)}
	m.NDCG10 = NDCG(ranked, grades, k)
	m.MRR = MRR(ranked, grades)
	m.Precision5 = PrecisionAt(ranked, grades, 5)
	m.Recall10 = RecallAt(ranked, grades, k)
	return m
}

// NDCG computes graded normalized discounted cumulative gain.
func NDCG(ranked []Ranked, grades map[string]int, k int) float64 {
	if k <= 0 || len(grades) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	dcg := 0.0
	seen := make(map[string]struct{}, k)
	for i := 0; i < k; i++ {
		if _, alreadyCounted := seen[ranked[i].File]; alreadyCounted {
			continue
		}
		if grade := grades[ranked[i].File]; grade > 0 {
			dcg += gain(grade) / math.Log2(float64(i+2))
			seen[ranked[i].File] = struct{}{}
		}
	}
	ideal := make([]int, 0, len(grades))
	for _, grade := range grades {
		ideal = append(ideal, grade)
	}
	// The number of relevant files is small in the benchmark schema; sorting a
	// local copy keeps the public Query order irrelevant to the ideal score.
	for i := 0; i < len(ideal); i++ {
		for j := i + 1; j < len(ideal); j++ {
			if ideal[j] > ideal[i] {
				ideal[i], ideal[j] = ideal[j], ideal[i]
			}
		}
	}
	if len(ideal) > k {
		ideal = ideal[:k]
	}
	idcg := 0.0
	for i, grade := range ideal {
		idcg += gain(grade) / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func gain(grade int) float64 { return math.Pow(2, float64(grade)) - 1 }

// MRR computes reciprocal rank of the first relevant result.
func MRR(ranked []Ranked, grades map[string]int) float64 {
	for i, r := range ranked {
		if grades[r.File] > 0 {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// PrecisionAt computes binary precision over the first k result positions.
func PrecisionAt(ranked []Ranked, grades map[string]int, k int) float64 {
	if k <= 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	if k == 0 {
		return 0
	}
	hits := 0
	seen := make(map[string]struct{}, k)
	for _, r := range ranked[:k] {
		if grades[r.File] <= 0 {
			continue
		}
		if _, alreadyCounted := seen[r.File]; !alreadyCounted {
			hits++
			seen[r.File] = struct{}{}
		}
	}
	return float64(hits) / float64(k)
}

// RecallAt computes binary recall over the first k result positions.
func RecallAt(ranked []Ranked, grades map[string]int, k int) float64 {
	if k <= 0 || len(grades) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	hits := 0
	seen := make(map[string]struct{}, k)
	for _, r := range ranked[:k] {
		if grades[r.File] > 0 {
			if _, ok := seen[r.File]; !ok {
				hits++
				seen[r.File] = struct{}{}
			}
		}
	}
	return float64(hits) / float64(len(grades))
}
