package schemaassets

import "embed"

//go:embed *.schema.json
var files embed.FS

func Read(name string) ([]byte, error) { return files.ReadFile(name) }
