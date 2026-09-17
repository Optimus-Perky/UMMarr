package api

import "github.com/Optimus-Perky/UMMarr/internal/store"

// NamingGroups exposes the naming guide's definition to package api_test,
// whose seeding helpers the resolver drift test needs.
var NamingGroups = namingGroups

// NamingFieldValue reads one naming box's saved template out of c.
func NamingFieldValue(f namingField, c store.NamingConfig) string { return f.value(c) }
