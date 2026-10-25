// Package kit holds what every kit carries besides its recipe.
package kit

import _ "embed"

// Restore is the generic restore procedure, written into every kit at backup.
//
//go:embed RESTORE.md
var Restore []byte
