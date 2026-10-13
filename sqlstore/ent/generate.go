//go:build ignore

// Command generate runs Ent codegen for connectionforge's SQL store.
// Run via: go run ./sqlstore/ent/generate.go
package main

import (
	"log"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	err := entc.Generate("./sqlstore/ent/schema", &gen.Config{
		Package: "github.com/plexusone/connectionforge/sqlstore/ent",
	})
	if err != nil {
		log.Fatalf("running ent codegen: %v", err)
	}
}
