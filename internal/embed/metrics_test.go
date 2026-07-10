package embed

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingObserver struct {
	calls []struct {
		model  string
		inputs int
		err    error
	}
}

func (r *recordingObserver) ObserveEmbed(model string, inputs int, _ time.Duration, err error) {
	r.calls = append(r.calls, struct {
		model  string
		inputs int
		err    error
	}{model, inputs, err})
}

// metricsStub is a minimal Embedder for decorator tests.
type metricsStub struct {
	err error
}

func (s metricsStub) ModelInfo(context.Context, string) (*ModelInfo, error) {
	return &ModelInfo{}, nil
}
func (s metricsStub) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return make([][]float32, len(inputs)), nil
}
func (s metricsStub) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return []float32{0}, s.err
}
func (s metricsStub) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestInstrument_NilPassThrough(t *testing.T) {
	e := metricsStub{}
	if got := Instrument(e, nil); got != Embedder(e) {
		t.Error("nil observer must return the embedder unchanged")
	}
	if got := Instrument(nil, &recordingObserver{}); got != nil {
		t.Error("nil embedder must return nil unchanged")
	}
}

func TestInstrument_ObservesBatchAndSingle(t *testing.T) {
	obs := &recordingObserver{}
	e := Instrument(metricsStub{}, obs)

	if _, err := e.Embed(context.Background(), "bge-m3", "a", "b", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EmbedSingle(context.Background(), "bge-m3", "x"); err != nil {
		t.Fatal(err)
	}
	if len(obs.calls) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obs.calls))
	}
	if obs.calls[0].inputs != 3 || obs.calls[0].model != "bge-m3" {
		t.Errorf("batch observation = %+v, want inputs=3 model=bge-m3", obs.calls[0])
	}
	if obs.calls[1].inputs != 1 {
		t.Errorf("single observation inputs = %d, want 1", obs.calls[1].inputs)
	}
}

func TestInstrument_ObservesError(t *testing.T) {
	obs := &recordingObserver{}
	e := Instrument(metricsStub{err: errors.New("provider down")}, obs)
	if _, err := e.Embed(context.Background(), "m", "a"); err == nil {
		t.Fatal("expected error to propagate")
	}
	if len(obs.calls) != 1 || obs.calls[0].err == nil {
		t.Fatalf("error should be observed: %+v", obs.calls)
	}
}

// TestInstrument_DelegatesUninstrumented confirms ModelInfo/ListModels pass
// through the decorator unchanged.
func TestInstrument_DelegatesUninstrumented(t *testing.T) {
	e := Instrument(metricsStub{}, &recordingObserver{})
	if _, err := e.ModelInfo(context.Background(), "m"); err != nil {
		t.Errorf("ModelInfo delegation: %v", err)
	}
	if _, err := e.ListModels(context.Background()); err != nil {
		t.Errorf("ListModels delegation: %v", err)
	}
}
