// Package safego starts UMMarr's background goroutines. Two things a bare
// `go f()` doesn't give:
//
//   - A panic is logged with its stack and the goroutine ends, rather than
//     taking the whole process - and every import, download check and
//     page request in flight with it - down.
//   - The goroutines are counted, so a shutdown can wait a while for an
//     import or a file copy to finish instead of cutting it off mid-write.
package safego

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/logging"
)

var running sync.WaitGroup

// Go runs fn in a new goroutine. name says what it was in the log if it
// panics.
func Go(name string, fn func()) {
	running.Add(1)
	go func() {
		defer running.Done()
		Run(name, fn)
	}()
}

// Run calls fn in the current goroutine, turning a panic into a logged
// error that it returns.
func Run(name string, fn func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%s panicked: %v", name, p)
			logging.Errorf("%v\n%s", err, debug.Stack())
		}
	}()
	fn()
	return nil
}

// Wait waits up to timeout for every goroutine started with Go to return,
// reporting whether they all did.
func Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		running.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
