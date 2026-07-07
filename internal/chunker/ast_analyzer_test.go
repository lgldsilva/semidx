package chunker

import (
	"testing"
)

func TestAnalyzeGoImports_SingleLocal(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	"github.com/lgldsilva/semidx/internal/chunker"
)

func main() {}
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	want := []string{"internal/chunker/"}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_LocalAndStdlib(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	"fmt"
	"os"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/pkg/client"
	"strings"
)
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	want := []string{"internal/chunker/", "pkg/client/"}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_ThirdPartyExcludedWithModulePath(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/spf13/cobra"
	"github.com/prometheus/client_golang/prometheus"
)
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	want := []string{"internal/store/"}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_ThirdPartyIncludedWithEmptyModulePath(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/spf13/cobra"
	"net/http"
)
`)
	got := AnalyzeGoImports(src, "")
	want := []string{
		"github.com/lgldsilva/semidx/internal/store/",
		"github.com/spf13/cobra/",
	}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_EmptyContent(t *testing.T) {
	t.Parallel()

	got := AnalyzeGoImports(nil, "github.com/lgldsilva/semidx")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}

	got = AnalyzeGoImports([]byte{}, "github.com/lgldsilva/semidx")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestAnalyzeGoImports_MalformedGo(t *testing.T) {
	t.Parallel()

	// Incomplete file — no package declaration, bare text.
	src := []byte(`this is not valid Go code at all`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestAnalyzeGoImports_Deduplication(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/pkg/client"
)
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	want := []string{"internal/chunker/", "pkg/client/"}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_SelfReferenceRoot(t *testing.T) {
	t.Parallel()

	// Importing the module root package itself should be skipped.
	src := []byte(`package main

import (
	"github.com/lgldsilva/semidx"
	"github.com/lgldsilva/semidx/internal/chunker"
)
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	want := []string{"internal/chunker/"}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_DotImportSkipped(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	. "github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/pkg/client"
)
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	want := []string{"pkg/client/"}
	assertSlice(t, got, want)
}

func TestAnalyzeGoImports_NoImportsReturnsNil(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

func main() {}
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestAnalyzeGoImports_OnlyStdlibReturnsNil(t *testing.T) {
	t.Parallel()

	src := []byte(`package main

import (
	"fmt"
	"strings"
)
`)
	got := AnalyzeGoImports(src, "github.com/lgldsilva/semidx")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// assertSlice is a test helper that compares two string slices and fails with a
// useful message if they differ (order-sensitive).
func assertSlice(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, len(want)=%d\ngot:  %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at index %d:\ngot:  %#v\nwant: %#v", i, got, want)
		}
	}
}
