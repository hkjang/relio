package webui

import "embed"

// Assets is populated by the React production build and embedded in the Go
// binary, so the runtime image contains no Node.js process.
//
//go:embed dist/*
var Assets embed.FS
