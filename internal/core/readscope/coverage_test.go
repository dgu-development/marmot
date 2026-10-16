package readscope

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// unscoped are the packages that read tables of visible with a plain pool,
// and why none of those reads reaches a caller as it is.
var unscoped = map[string]string{
	"internal/core/domain":     "decides the scope itself; the API serves it through GuardReads",
	"internal/core/enrichment": "evaluates rules on ingestion, for the server",
	"internal/core/quality":    "scores the whole catalogue for the server; who serves the scores filters them",
	"internal/telemetry":       "counts for anonymous telemetry",
}

// A store that serves callers and reads a table of visible must do it through
// Scoped; this fails when a new one is added without it.
func TestEveryReaderOfADomainTableIsScoped(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	reads := regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+(` + strings.Join(tableNames, "|") + `)\b`)
	readers := map[string]bool{}
	scoped := map[string]bool{}
	for _, top := range []string{"internal", "pkg", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			dir, _ := filepath.Rel(root, filepath.Dir(path))
			dir = filepath.ToSlash(dir)
			if reads.Match(source) {
				readers[dir] = true
			}
			if strings.Contains(string(source), "readscope.Scoped(") {
				scoped[dir] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(readers) < 10 {
		t.Fatalf("only %d packages found reading domain tables; the walk is looking in the wrong place", len(readers))
	}
	for dir := range readers {
		if !scoped[dir] && unscoped[dir] == "" {
			t.Errorf("%s reads tables of a domain with a pool that is not readscope.Scoped", dir)
		}
	}
	for dir := range unscoped {
		if !readers[dir] {
			t.Errorf("%s no longer reads tables of a domain; drop it from unscoped", dir)
		}
	}
}
