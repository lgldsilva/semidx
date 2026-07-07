package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchmarkAllLanguages(t *testing.T) {
	projects := []struct {
		path string
		max  int
	}{
		{"/Volumes/MSD512/Projetos/semidx", 200},
		{"/Volumes/MSD512/Projetos/jackui", 200},
		{"/Volumes/MSD512/Projetos/MLins", 200},
	}

	for _, proj := range projects {
		name := filepath.Base(proj.path)
		if _, err := os.Stat(proj.path); err != nil {
			t.Logf("skip %s: %v", name, err)
			continue
		}
		t.Run(name, func(t *testing.T) {
			benchProject(t, proj.path, proj.max)
		})
	}
}

func benchProject(t *testing.T, root string, maxFiles int) {
	type langStats struct {
		total    int
		withSym  int
		noSym    int
		errors   int
		totalSym int
	}
	extStats := map[string]*langStats{
		".go":    {},
		".java":  {},
		".kt":    {},
		".scala": {},
		".js":    {},
		".jsx":   {},
		".mjs":   {},
		".cjs":   {},
		".ts":    {},
		".tsx":   {},
		".py":    {},
		".tf":    {},
	}

	total := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(base, ".") || base == "node_modules" ||
				base == "vendor" || base == "dist" || base == "build" ||
				base == "target" || base == ".next" || base == "__pycache__" ||
				base == "testdata" || base == ".terraform" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(base, "_test.go") {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(base))
		st, ok := extStats[ext]
		if !ok {
			return nil
		}

		if total >= maxFiles {
			return filepath.SkipAll
		}
		total++
		st.total++

		content, err := os.ReadFile(p)
		if err != nil {
			st.errors++
			return nil
		}
		if len(content) > 500_000 {
			return nil
		}

		syms := Symbols(p, content)
		if len(syms) > 0 {
			st.withSym++
			st.totalSym += len(syms)
		} else {
			st.noSym++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	t.Logf("Total files: %d", total)
	t.Logf("%-8s %7s %7s %7s %7s %7s %7s",
		"Ext", "Total", "w/Syms", "noSym", "Err", "TotSym", "Avg")
	for ext, st := range extStats {
		if st.total == 0 {
			continue
		}
		avg := 0.0
		if st.withSym > 0 {
			avg = float64(st.totalSym) / float64(st.withSym)
		}
		t.Logf("%-8s %7d %7d %7d %7d %7d %7.1f",
			ext, st.total, st.withSym, st.noSym, st.errors, st.totalSym, avg)
	}

	withSym := 0
	for _, st := range extStats {
		withSym += st.withSym
	}
	pct := 0.0
	if total > 0 {
		pct = float64(withSym) / float64(total) * 100
	}
	t.Logf("Coverage: %d/%d = %.1f%% files with symbols", withSym, total, pct)
}
