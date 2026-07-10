package localstore

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
)

func TestCreateProjectAndLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	p, err := s.CreateProject(ctx, "git-proj", "bge-m3", "git", "https://example.com/r.git", "main", 3)
	if err != nil || p == nil || p.Name != "git-proj" {
		t.Fatalf("CreateProject = %+v err=%v", p, err)
	}
	byName, err := s.GetProject(ctx, "git-proj")
	if err != nil || byName.ID != p.ID {
		t.Fatalf("GetProject = %+v err=%v", byName, err)
	}
	byID, err := s.GetProjectByID(ctx, p.ID)
	if err != nil || byID.Name != "git-proj" {
		t.Fatalf("GetProjectByID = %+v err=%v", byID, err)
	}
	if byID, err := s.GetProjectByIdentity(ctx, p.Identity); err != nil || byID.ID != p.ID {
		t.Fatalf("GetProjectByIdentity = %+v err=%v", byID, err)
	}
}

func TestFetchChunksByPathAndDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "chunks", "/tmp/ch", "bge-m3", 3)
	fileID, _ := s.UpsertFile(ctx, projectID, "pkg/a.go", "h1", 10)
	chunks := []chunker.Chunk{{Content: "func main(){}", StartLine: 1, EndLine: 1}}
	embs := [][]float32{{1, 0, 0}}
	if err := s.InsertChunks(ctx, projectID, fileID, chunks, embs, 3); err != nil {
		t.Fatal(err)
	}

	byPath, err := s.FetchChunksByPath(ctx, projectID, "pkg/a.go", 3, 10)
	if err != nil || len(byPath) != 1 {
		t.Fatalf("FetchChunksByPath = %+v err=%v", byPath, err)
	}
	byDir, err := s.FetchChunksByDirPrefix(ctx, projectID, "pkg/", 3, 10)
	if err != nil || len(byDir) != 1 {
		t.Fatalf("FetchChunksByDirPrefix = %+v err=%v", byDir, err)
	}
}

func TestSearchSimilarEmptyEmbedding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "sim", "/tmp/sim", "bge-m3", 3)
	results, err := s.SearchSimilar(ctx, projectID, nil, 3, 5)
	if err != nil || results != nil {
		t.Fatalf("SearchSimilar(nil) = %+v err=%v", results, err)
	}
}
