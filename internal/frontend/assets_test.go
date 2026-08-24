package frontend

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

var assetReference = regexp.MustCompile(`(?:src|href)="/([^"?#]+)`)

func TestAssetsContainCompiledApplicationAndLanding(t *testing.T) {
	assets := Assets()
	for _, name := range []string{"index.html", "landing.html"} {
		page, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(page, []byte("/src/")) {
			t.Fatalf("embedded %s references Vite development source", name)
		}
		matches := assetReference.FindAllSubmatch(page, -1)
		if len(matches) == 0 {
			t.Fatalf("embedded %s does not reference compiled assets", name)
		}
		for _, match := range matches {
			if _, err := fs.Stat(assets, string(match[1])); err != nil {
				t.Errorf("embedded %s references missing asset %q: %v", name, match[1], err)
			}
		}
	}
	landing, err := fs.ReadFile(assets, "landing.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(landing, []byte("__ONBOARDD_SETUP_URL__")) {
		t.Fatal("embedded landing page is missing the runtime setup URL placeholder")
	}
}
