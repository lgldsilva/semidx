package localstore

import (
	"context"
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestEncodeDecodeRoundTripProperty proves the float32 BLOB codec is a lossless,
// bit-exact round trip for ANY vector — including NaN/Inf/negative-zero, which a
// naive value comparison would miss. Kills mutations to the byte offsets or
// endianness in encode/decodeEmbedding.
func TestEncodeDecodeRoundTripProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 256).Draw(rt, "n")
		vec := make([]float32, n)
		for i := range vec {
			vec[i] = rapid.Float32().Draw(rt, "v")
		}

		encoded := encodeEmbedding(vec)
		if len(encoded) != n*4 {
			rt.Fatalf("encoded %d bytes for %d floats, want %d", len(encoded), n, n*4)
		}
		got := decodeEmbedding(encoded)
		if len(got) != len(vec) {
			rt.Fatalf("decoded len = %d, want %d", len(got), len(vec))
		}
		for i := range vec {
			// Bit-exact comparison so NaN and -0.0 are handled correctly.
			if math.Float32bits(got[i]) != math.Float32bits(vec[i]) {
				rt.Fatalf("round-trip[%d]: got bits %#x, want %#x", i,
					math.Float32bits(got[i]), math.Float32bits(vec[i]))
			}
		}
	})
}

// drawFiniteVec draws an n-dim vector of finite float32 values; component 0 is
// guaranteed non-zero so the vector is never the zero vector.
func drawFiniteVec(rt *rapid.T, n int, label string) []float32 {
	v := make([]float32, n)
	v[0] = rapid.Float32Range(0.5, 1000).Draw(rt, label+"0")
	for i := 1; i < n; i++ {
		v[i] = rapid.Float32Range(-1000, 1000).Draw(rt, label)
	}
	return v
}

// TestCosineSimilarityProperty asserts the mathematical invariants of cosine:
// self-similarity is 1, symmetry, and the result is bounded to [-1, 1].
func TestCosineSimilarityProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 64).Draw(rt, "n")
		a := drawFiniteVec(rt, n, "a")
		b := drawFiniteVec(rt, n, "b")

		if self := cosineSimilarity(a, a); math.Abs(self-1) > 1e-4 {
			rt.Fatalf("cosine(a,a) = %v, want 1", self)
		}
		ab := cosineSimilarity(a, b)
		ba := cosineSimilarity(b, a)
		if math.Abs(ab-ba) > 1e-6 {
			rt.Fatalf("cosine not symmetric: %v vs %v", ab, ba)
		}
		if ab < -1-1e-4 || ab > 1+1e-4 {
			rt.Fatalf("cosine(a,b) = %v, out of [-1,1]", ab)
		}
	})
}

// TestSearchSimilarTopKProperty proves SearchSimilar never returns more than
// topK results and, when topK<=0, returns everything embedded — over a fixed
// corpus with many random topK draws.
func TestSearchSimilarTopKProperty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	pid, _ := s.UpsertProject(ctx, "p", "/p", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, "f.go", "h", 1)

	const total = 25
	chunks := make([]chunker.Chunk, total)
	embs := make([][]float32, total)
	for i := 0; i < total; i++ {
		chunks[i] = chunker.Chunk{Content: "chunk", StartLine: i + 1, EndLine: i + 1}
		embs[i] = []float32{float32(i), 1, 0}
	}
	if err := s.InsertChunks(ctx, pid, fid, chunks, embs, 3); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	rapid.Check(t, func(rt *rapid.T) {
		topK := rapid.IntRange(-3, total+10).Draw(rt, "topK")
		res, err := s.SearchSimilar(ctx, pid, []float32{1, 1, 0}, 3, topK)
		if err != nil {
			rt.Fatalf("SearchSimilar: %v", err)
		}
		if topK > 0 && len(res) > topK {
			rt.Fatalf("returned %d results, exceeds topK=%d", len(res), topK)
		}
		if topK <= 0 && len(res) != total {
			rt.Fatalf("topK<=0 returned %d, want all %d", len(res), total)
		}
		// Results must always be sorted by descending score.
		for i := 1; i < len(res); i++ {
			if res[i-1].Score < res[i].Score {
				rt.Fatalf("results not sorted at %d: %v < %v", i, res[i-1].Score, res[i].Score)
			}
		}
	})
}

// TestKeywordSearchTopKProperty: the keyword path also honours topK.
func TestKeywordSearchTopKProperty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	pid, _ := s.UpsertProject(ctx, "p", "/p", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, "f.go", "h", 1)

	const total = 20
	chunks := make([]chunker.Chunk, total)
	embs := make([][]float32, total)
	for i := 0; i < total; i++ {
		chunks[i] = chunker.Chunk{Content: "needle marker", StartLine: i + 1, EndLine: i + 1}
		embs[i] = []float32{1, 0}
	}
	if err := s.InsertChunks(ctx, pid, fid, chunks, embs, 2); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	rapid.Check(t, func(rt *rapid.T) {
		topK := rapid.IntRange(1, total+5).Draw(rt, "topK")
		res, err := s.SearchSimilarKeywords(ctx, pid, "needle", 2, topK)
		if err != nil {
			rt.Fatalf("SearchSimilarKeywords: %v", err)
		}
		if len(res) > topK {
			rt.Fatalf("keyword search returned %d, exceeds topK=%d", len(res), topK)
		}
		for _, r := range res {
			if r.Score != 0.5 {
				rt.Fatalf("keyword score = %v, want constant 0.5", r.Score)
			}
		}
	})
}
