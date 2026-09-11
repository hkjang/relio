package webui

import "embed"

// Assets is populated by the React production build and embedded in the Go
// binary, so the runtime image contains no Node.js process.
//
// The build output is not committed, but an embed pattern must match at least
// one file or the package does not compile. dist/README is committed for that
// reason alone (and web/public/README puts it back after every build), so a
// fresh checkout builds and tests before the frontend has been built.
//
//go:embed dist/*
var Assets embed.FS
