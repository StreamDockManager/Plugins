package engine

import _ "embed"

//go:embed palette.v1.json
var paletteJSON []byte

// PaletteJSON exposes generated theme data to package tooling
func PaletteJSON() []byte { return append([]byte(nil), paletteJSON...) }
