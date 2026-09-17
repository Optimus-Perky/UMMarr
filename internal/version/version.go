// Package version says which build this is. The deploy step writes
// version.txt ("<git describe> <date>"); a source build reports "dev".
package version

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var raw string

// Commit is the short git revision the binary was built from ("dev" when
// unknown); Built is when it was built ("" when unknown).
var Commit, Built = parse(raw)

func parse(s string) (string, string) {
	fields := strings.Fields(s)
	switch len(fields) {
	case 0:
		return "dev", ""
	case 1:
		return fields[0], ""
	}
	return fields[0], fields[1]
}
