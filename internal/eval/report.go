package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// MarshalResults returns the stable, indented JSON representation used by CI
// artifacts and harnesses.
func MarshalResults(results Results) ([]byte, error) {
	if results.Version == 0 {
		results.Version = CurrentDatasetVersion
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal benchmark results: %w", err)
	}
	b = append(b, '\n')
	return b, nil
}

// WriteResults writes an artifact without exposing partial output on marshal
// failure. The caller chooses the destination and is responsible for policy
// around overwriting an existing baseline.
func WriteResults(path string, results Results) error {
	b, err := MarshalResults(results)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write benchmark results: %w", err)
	}
	return nil
}

// LoadResults reads one benchmark artifact and rejects malformed or trailing
// JSON so CI never compares a partially written file.
func LoadResults(path string) (Results, error) {
	// #nosec G304 -- path is an explicit CLI-selected benchmark artifact.
	f, err := os.Open(path)
	if err != nil {
		return Results{}, fmt.Errorf("open benchmark results: %w", err)
	}
	defer func() { _ = f.Close() }()
	var results Results
	dec := json.NewDecoder(f)
	if err := dec.Decode(&results); err != nil {
		return Results{}, fmt.Errorf("decode benchmark results: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Results{}, err
	}
	if results.Version < 1 || results.Version > CurrentDatasetVersion {
		return Results{}, fmt.Errorf("unsupported benchmark result version %d", results.Version)
	}
	return results, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var trailing any
	err := dec.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("benchmark results contain trailing JSON")
	}
	return fmt.Errorf("decode trailing benchmark results: %w", err)
}

// Compare checks that two artifacts describe the same evaluation contract and
// returns candidate-minus-baseline metric deltas.
func Compare(baseline, candidate Results) (Results, error) {
	if baseline.Metadata.DatasetSHA256 == "" || candidate.Metadata.DatasetSHA256 == "" {
		return Results{}, fmt.Errorf("comparison requires dataset_sha256 in both artifacts")
	}
	if baseline.Metadata.IndexFingerprint == "" || candidate.Metadata.IndexFingerprint == "" {
		return Results{}, fmt.Errorf("comparison requires index_fingerprint in both artifacts")
	}
	if baseline.Metadata.DatasetSHA256 != candidate.Metadata.DatasetSHA256 {
		return Results{}, fmt.Errorf("incompatible datasets: %q != %q", baseline.Metadata.DatasetSHA256, candidate.Metadata.DatasetSHA256)
	}
	if baseline.Metadata.IndexFingerprint != candidate.Metadata.IndexFingerprint {
		return Results{}, fmt.Errorf("incompatible index fingerprints: %q != %q", baseline.Metadata.IndexFingerprint, candidate.Metadata.IndexFingerprint)
	}
	if err := compareRuntimeContract(baseline.Metadata, candidate.Metadata); err != nil {
		return Results{}, err
	}
	return Results{
		Version:      CurrentDatasetVersion,
		Metadata:     candidate.Metadata,
		Total:        candidate.Total,
		Failed:       candidate.Failed - baseline.Failed,
		Fallbacks:    candidate.Fallbacks - baseline.Fallbacks,
		NDCG10:       candidate.NDCG10 - baseline.NDCG10,
		MRR:          candidate.MRR - baseline.MRR,
		Precision5:   candidate.Precision5 - baseline.Precision5,
		Recall10:     candidate.Recall10 - baseline.Recall10,
		LatencyP50MS: candidate.LatencyP50MS - baseline.LatencyP50MS,
		LatencyP95MS: candidate.LatencyP95MS - baseline.LatencyP95MS,
		LatencyP99MS: candidate.LatencyP99MS - baseline.LatencyP99MS,
	}, nil
}

func compareRuntimeContract(baseline, candidate RunMetadata) error {
	checks := []struct {
		name, baseline, candidate string
	}{
		{name: "backend", baseline: baseline.Backend, candidate: candidate.Backend},
		{name: "model", baseline: baseline.Model, candidate: candidate.Model},
		{name: "project identity", baseline: baseline.ProjectIdentity, candidate: candidate.ProjectIdentity},
		{name: "mode", baseline: baseline.Mode, candidate: candidate.Mode},
	}
	for _, check := range checks {
		if check.baseline != check.candidate {
			return fmt.Errorf("incompatible %s: %q != %q", check.name, check.baseline, check.candidate)
		}
	}
	if baseline.Dimensions != candidate.Dimensions {
		return fmt.Errorf("incompatible dimensions: %d != %d", baseline.Dimensions, candidate.Dimensions)
	}
	if baseline.Seed != candidate.Seed {
		return fmt.Errorf("incompatible seeds: %d != %d", baseline.Seed, candidate.Seed)
	}
	if baseline.Runs != candidate.Runs {
		return fmt.Errorf("incompatible run counts: %d != %d", baseline.Runs, candidate.Runs)
	}
	return nil
}
