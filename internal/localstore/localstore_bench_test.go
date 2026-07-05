package localstore

import (
	"math/rand"
	"testing"
)

func BenchmarkCosineBruteForce_768d(b *testing.B) {
	dims := 768
	query := make([]float32, dims)
	candidate := make([]float32, dims)
	for i := range query {
		query[i] = rand.Float32()
		candidate[i] = rand.Float32()
	}
	b.ResetTimer()
	for b.Loop() {
		cosineSimilarity(query, candidate)
	}
}

func BenchmarkCosineBruteForce_1024d(b *testing.B) {
	dims := 1024
	query := make([]float32, dims)
	candidate := make([]float32, dims)
	for i := range query {
		query[i] = rand.Float32()
		candidate[i] = rand.Float32()
	}
	b.ResetTimer()
	for b.Loop() {
		cosineSimilarity(query, candidate)
	}
}

func BenchmarkDecodeEmbedding_768d(b *testing.B) {
	dims := 768
	floats := make([]float32, dims)
	for i := range floats {
		floats[i] = rand.Float32()
	}
	blob := encodeEmbedding(floats)
	b.ResetTimer()
	for b.Loop() {
		decodeEmbedding(blob)
	}
}
