// Package buildinfo carries the gateway version, injectable at link time:
//
//	go build -ldflags="-X github.com/EffNine/conductor/internal/buildinfo.Version=$(VERSION)"
package buildinfo

// Version is the gateway version. "dev" unless overridden via -ldflags.
var Version = "dev"
