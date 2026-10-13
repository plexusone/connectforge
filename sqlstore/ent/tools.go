//go:build tools

// Package ent pins the code generator's dependencies, which only the
// build-ignored generate.go imports, so go mod tidy keeps them.
package ent

import (
	_ "entgo.io/ent/entc"
	_ "entgo.io/ent/entc/gen"
)
