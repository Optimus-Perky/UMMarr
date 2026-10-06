// Package logging is UMMarr's leveled log. Every message has a severity,
// and the log level - Settings -> General -> Logging, or UMMARR_LOG_LEVEL -
// decides which severities are written:
//
//	Standard    errors, warnings and the normal running commentary
//	            (Errorf, Warnf, Infof). The default.
//	Verbose     adds the reasoning behind what UMMarr does: which indexers a
//	            search asked or skipped, which download client a grab went
//	            to, when each task ran (Debugf).
//	Diagnostic  adds everything else: every outbound HTTP request, every
//	            release a search rejected and why, every page request
//	            (Tracef). For chasing a problem, not for leaving on.
//
// The level can change while UMMarr runs. Messages go through the standard
// log package, tagged "[warn]" and so on, so the System -> Logs page and
// the log file can tell severities apart without guessing from the words.
package logging

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
)

// Level is how much gets logged.
type Level int32

const (
	Standard Level = iota
	Verbose
	Diagnostic
)

// Levels are the choices, least to most.
var Levels = []Level{Standard, Verbose, Diagnostic}

// String is the level's name as settings store it.
func (l Level) String() string {
	switch l {
	case Verbose:
		return "verbose"
	case Diagnostic:
		return "diagnostic"
	}
	return "standard"
}

// Label is the level as the UI shows it.
func (l Level) Label() string {
	switch l {
	case Verbose:
		return "Verbose"
	case Diagnostic:
		return "Diagnostic"
	}
	return "Standard"
}

// ParseLevel reads a level's name; "debug" and "trace" are accepted for
// Verbose and Diagnostic, the words other tools use.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "standard", "info", "":
		return Standard, true
	case "verbose", "debug":
		return Verbose, true
	case "diagnostic", "trace":
		return Diagnostic, true
	}
	return Standard, false
}

var current atomic.Int32

// SetLevel changes the level from now on.
func SetLevel(l Level) { current.Store(int32(l)) }

// CurrentLevel is the level in force.
func CurrentLevel() Level { return Level(current.Load()) }

// Enabled reports whether messages needing level l are written - worth
// checking before building an expensive one.
func Enabled(l Level) bool { return CurrentLevel() >= l }

// Severity tags, as they appear in a logged line.
const (
	TagError = "[error] "
	TagWarn  = "[warn] "
	TagInfo  = "[info] "
	TagDebug = "[verbose] "
	TagTrace = "[diagnostic] "
)

func output(tag, format string, args []any) {
	_ = log.Output(3, tag+fmt.Sprintf(format, args...))
}

// Errorf logs something that went wrong and needs attention.
func Errorf(format string, args ...any) { output(TagError, format, args) }

// Warnf logs something that went wrong but was worked around, or may need
// attention later.
func Warnf(format string, args ...any) { output(TagWarn, format, args) }

// Infof logs the normal running commentary.
func Infof(format string, args ...any) { output(TagInfo, format, args) }

// Debugf logs the reasoning behind a decision, at Verbose and above.
func Debugf(format string, args ...any) {
	if Enabled(Verbose) {
		output(TagDebug, format, args)
	}
}

// Tracef logs fine detail, at Diagnostic only.
func Tracef(format string, args ...any) {
	if Enabled(Diagnostic) {
		output(TagTrace, format, args)
	}
}
