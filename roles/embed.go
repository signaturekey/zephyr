package roleassets

import "embed"

//go:embed *.md
var files embed.FS

func Read(name string) ([]byte, error) { return files.ReadFile(name) }
