package installer

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// TestMain runs the window smoke test instead of the unit tests when
// FIRN_GUI_WINDOW_TEST names a PNG to write: it opens the real demo window
// on the desktop, as Run does, captures it once the disk scan has landed,
// and quits. It needs a display, so it is opt-in.
func TestMain(m *testing.M) {
	if out := os.Getenv("FIRN_GUI_WINDOW_TEST"); out != "" {
		os.Exit(windowSmoke(out))
	}
	os.Exit(m.Run())
}

func windowSmoke(out string) int {
	a, err := newApp(Options{Demo: true, Version: "smoke"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer a.cleanup()
	code := 0
	mygo.App.WhenReady(func() {
		win := a.openWindow()
		go func() {
			defer mygo.App.Quit()
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				done := make(chan bool, 1)
				win.Update(func() { done <- len(a.disks) > 0 })
				if <-done {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			win.Update(func() { a.next() }) // welcome -> image
			time.Sleep(500 * time.Millisecond)
			png, err := win.CapturePage()
			if err == nil {
				err = os.WriteFile(out, png, 0o644)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "capture:", err)
				code = 1
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		return 1
	}
	return code
}
