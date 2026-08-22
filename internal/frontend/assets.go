// Package frontend contains the compiled setup application embedded in onboardd.
package frontend

import (
	"embed"
	"io/fs"
)

// compiled contains only Vite's production output. Development mocks and TypeScript
// sources remain in the frontend directory and cannot be included in the appliance.
//
//go:embed dist
var compiled embed.FS

// Assets returns the compiled frontend rooted at its index.html.
func Assets() fs.FS {
	assets, err := fs.Sub(compiled, "dist")
	if err != nil {
		panic(err)
	}
	return assets
}
