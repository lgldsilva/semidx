package indexing_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/eval"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/search"
)

type deterministicEmbedder struct{}

func (deterministicEmbedder) basis(text string) []float32 {
	switch {
	case strings.Contains(text, "alpha"):
		return []float32{1, 0, 0}
	case strings.Contains(text, "beta"):
		return []float32{0, 1, 0}
	default:
		return []float32{0, 0, 1}
	}
}

func (e deterministicEmbedder) ModelInfo(context.Context, string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: "m", Dims: 3}, nil
}
func (e deterministicEmbedder) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, input := range inputs {
		out[i] = e.basis(input)
	}
	return out, nil
}
func (e deterministicEmbedder) EmbedSingle(_ context.Context, _, text string) ([]float32, error) {
	return e.basis(text), nil
}
func (deterministicEmbedder) ListModels(context.Context) ([]string, error) {
	return []string{"m"}, nil
}

func TestDeterministicVectorEvaluationOnSQLiteIsRepeatable(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "evaluation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	for name, content := range map[string]string{
		"alpha.txt": "alpha authentication behavior\n",
		"beta.txt":  "beta cache behavior\n",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	projectID, err := st.UpsertProject(ctx, "fixture", src, "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	emb := deterministicEmbedder{}
	idx := indexing.NewIndexer(st, emb, 3, indexing.IndexerOpts{
		Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 8,
	})
	if _, err := idx.IndexProject(ctx, projectID, src, "m", 0); err != nil {
		t.Fatal(err)
	}

	dataset := eval.Dataset{Version: 2, Queries: []eval.Query{
		{ID: "alpha", Query: "alpha", Relevant: []eval.Relevance{{File: "alpha.txt", Grade: 3}}},
		{ID: "beta", Query: "beta", Relevant: []eval.Relevance{{File: "beta.txt", Grade: 3}}},
	}}
	svc := search.NewService(st, emb)
	run := func() eval.Results {
		return eval.Run(ctx, dataset, eval.RunnerConfig{
			Runs: 3, TopK: 2, Seed: 42,
			Metadata: eval.RunMetadata{Mode: "vector", Environment: "deterministic-test"},
		}, func(ctx context.Context, query eval.Query) (eval.Observation, error) {
			response, err := svc.Search(ctx, search.Request{
				Project: "fixture", Query: query.Query, TopK: 2, VectorOnly: true,
			})
			if err != nil {
				return eval.Observation{}, err
			}
			ranked := make([]eval.Ranked, len(response.Results))
			for i, result := range response.Results {
				ranked[i] = eval.Ranked{File: result.FilePath}
			}
			return eval.Observation{
				Ranked: ranked, Backend: "sqlite", Model: response.Model,
				Dimensions: 3, Project: "fixture", ProjectIdentity: "path:fixture",
				IndexFingerprint: "deterministic-fixture",
			}, nil
		})
	}
	baseline, candidate := run(), run()
	if baseline.Failed != 0 || baseline.NDCG10 != 1 || baseline.MRR != 1 {
		t.Fatalf("deterministic baseline = %+v", baseline)
	}
	delta, err := eval.Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if delta.NDCG10 != 0 || delta.MRR != 0 || delta.Precision5 != 0 || delta.Recall10 != 0 {
		t.Fatalf("repeated evaluation changed metrics: %+v", delta)
	}
}
