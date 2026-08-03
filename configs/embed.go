package configassets

import "embed"

//go:embed default.yaml
var FS embed.FS

func ReadDefault() ([]byte, error) { return FS.ReadFile("default.yaml") }
