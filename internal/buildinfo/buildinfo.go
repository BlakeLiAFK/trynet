// Package buildinfo holds release metadata injected by the build workflow.
package buildinfo

// Version defaults to the first CLI release for ordinary go build invocations.
var Version = "1.0.0"

// Commit is "dev" for local builds without injected source metadata.
var Commit = "dev"
