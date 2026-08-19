// Package buildinfo contains values injected into release builds.
package buildinfo

// Version is replaced through -ldflags for release builds.
var Version = "development"
