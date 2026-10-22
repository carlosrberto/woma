package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const mod = "github.com/carlosrberto/woma/"

// The repo's shape holds: git, fsutil and config are leaves that import
// nothing of woma; core builds on them and knows nothing of the UI; tui
// builds on core; testutil is for tests only.
func TestImports(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != "." {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		test := strings.HasSuffix(path, "_test.go")
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			rel, ours := strings.CutPrefix(p, mod)
			if !ours {
				continue
			}
			bad := ""
			switch {
			case rel == "internal/testutil":
				if !test {
					bad = "testutil is for tests only"
				}
			case dir == "internal/git" || dir == "internal/fsutil" || dir == "internal/config" || dir == "internal/testutil":
				bad = dir + " imports nothing of woma"
			case dir == "internal/core" && rel == "internal/tui":
				bad = "core knows nothing of the UI"
			}
			if bad != "" {
				t.Errorf("%s imports %s: %s", path, p, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
