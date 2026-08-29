// Package extension ships the pi extension that gives agents the pib tools.
// It is embedded in the binary so pib stays a single artifact and the
// extension can never drift from the version of pib that spawned it.
package extension

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed pib.ts
var source []byte

// FileName is the extension's name on disk. pi loads TypeScript directly, so
// there is no build step.
const FileName = "pib.ts"

// Install writes the extension into the workspace directory and returns the
// path to pass to pi. An unchanged file is left alone so that a pi process
// already loading it is not disturbed.
func Install(stateDir string) (string, error) {
	dir := filepath.Join(stateDir, "extension")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, FileName)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, source) {
		return path, nil
	}
	if err := os.WriteFile(path, source, 0o644); err != nil {
		return "", err
	}

	return path, nil
}
