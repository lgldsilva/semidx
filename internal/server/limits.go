package server

import "github.com/lgldsilva/semidx/internal/indexing"

// IndexLimits configures resource caps applied to server-side indexing jobs.
// Zero values fall back to indexer defaults (unlimited project caps).
type IndexLimits struct {
	MaxFileSize         int
	MaxChunksPerFile    int
	MaxChunksPerProject int
	MaxFilesPerProject  int
}

// SetIndexLimits applies indexing resource caps for background jobs and push ingest.
func (s *Server) SetIndexLimits(l IndexLimits) {
	s.indexLimits = l
}

func (s *Server) indexerOpts() indexing.IndexerOpts {
	l := s.indexLimits
	return indexing.IndexerOpts{
		MaxFileSize:         l.MaxFileSize,
		MaxChunksPerFile:    l.MaxChunksPerFile,
		MaxChunksPerProject: l.MaxChunksPerProject,
		MaxFilesPerProject:  l.MaxFilesPerProject,
	}
}

func (s *Server) indexerOptsWithProgress(onProgress indexing.IndexProgressFunc) indexing.IndexerOpts {
	o := s.indexerOpts()
	o.OnProgress = onProgress
	return o
}

func (s *Server) indexerOptsForJob(jobType string, onProgress indexing.IndexProgressFunc) indexing.IndexerOpts {
	o := s.indexerOptsWithProgress(onProgress)
	o.GitMode = jobType == "git_history"
	o.GitSince = "30.days"
	return o
}
