package safego_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/safego"
)

func TestRun_TurnsAPanicIntoAnError(t *testing.T) {
	err := safego.Run("boom", func() { panic("nil map") })
	if err == nil || !strings.Contains(err.Error(), "boom panicked: nil map") {
		t.Fatalf("want the panic as an error, got %v", err)
	}
	if err := safego.Run("fine", func() {}); err != nil {
		t.Fatalf("want nil for a normal return, got %v", err)
	}
}

func TestGo_PanicDoesNotCrashAndWaitSeesItFinish(t *testing.T) {
	release := make(chan struct{})
	safego.Go("slow", func() { <-release })
	safego.Go("panics", func() { panic("background bug") })
	if safego.Wait(20 * time.Millisecond) {
		t.Fatal("want Wait to time out while a goroutine is still running")
	}
	close(release)
	if !safego.Wait(time.Second) {
		t.Fatal("want Wait to return once every goroutine has finished")
	}
}
