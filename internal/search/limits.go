package search

// MaxGraphDepth caps BFS depth for Graph-RAG expansion (DoS guard).
const MaxGraphDepth = 5

// DefaultGraphDepth is used when graph_depth is unset or non-positive.
const DefaultGraphDepth = 2

// ClampGraphDepth normalises a requested graph depth to [1, MaxGraphDepth].
func ClampGraphDepth(depth int) int {
	if depth <= 0 {
		return DefaultGraphDepth
	}
	if depth > MaxGraphDepth {
		return MaxGraphDepth
	}
	return depth
}
