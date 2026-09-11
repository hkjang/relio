package webui

import (
	"bytes"
	"io/fs"
	"os"
	"testing"
)

// The embed pattern has to match before the frontend has been built, so the
// checkout carries one anchor file under dist/. Every web build empties that
// directory and copies web/public/ back in, so the anchor must exist there
// too, byte for byte — otherwise a build would leave the checkout dirty.
func TestEmbedAnchorIsRestoredByTheWebBuild(t *testing.T) {
	embedded, err := fs.ReadFile(Assets, "dist/README")
	if err != nil {
		t.Fatalf("dist/README is not embedded; a fresh checkout could not build: %v", err)
	}
	public, err := os.ReadFile("../../web/public/README")
	if err != nil {
		t.Fatalf("web/public/README must exist so the web build restores the anchor: %v", err)
	}
	if !bytes.Equal(embedded, public) {
		t.Fatal("internal/webui/dist/README and web/public/README differ; the web build would rewrite the committed copy")
	}
}
