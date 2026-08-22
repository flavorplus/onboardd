package frontend

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

var assetReference = regexp.MustCompile(`(?:src|href)="/([^"?#]+)`)

func TestAssetsContainCompiledApplication(t *testing.T) {
	assets := Assets()
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(index, []byte("/src/main.ts")) {
		t.Fatal("embedded index references Vite development source")
	}
	matches := assetReference.FindAllSubmatch(index, -1)
	if len(matches) == 0 {
		t.Fatal("embedded index does not reference compiled assets")
	}
	for _, match := range matches {
		if _, err := fs.Stat(assets, string(match[1])); err != nil {
			t.Errorf("embedded index references missing asset %q: %v", match[1], err)
		}
	}
}
