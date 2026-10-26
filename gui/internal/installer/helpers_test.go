package installer

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
)

// testQueue stands in for Window.Update: background work queues its state
// changes, and the test applies them on its own goroutine.
type testQueue struct {
	mu  sync.Mutex
	fns []func()
}

func (q *testQueue) post(fn func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fns = append(q.fns, fn)
}

func (q *testQueue) drain() int {
	q.mu.Lock()
	fns := q.fns
	q.fns = nil
	q.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	return len(fns)
}

// newTestApp builds a demo app whose simulated delays are zero and whose
// background results arrive through a testQueue.
func newTestApp(t *testing.T, o Options) (*app, *testQueue) {
	t.Helper()
	o.Demo = true
	a, err := newApp(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.cleanup)
	a.be.(*demoBackend).pace = 0
	q := &testQueue{}
	a.post = q.post
	a.quit = func() { a.status = "quit" }
	return a, q
}

// ready loads the demo disks and the chosen image's preview synchronously.
func ready(t *testing.T, a *app) {
	t.Helper()
	disks, err := a.be.Disks(context.Background())
	a.setDisks(disks, err)
	a.setPreview(a.be.InspectCore(context.Background(), a.c.entry.Ref))
}

// completeAnswers fills in everything the pages require.
func completeAnswers(a *app) {
	a.c.setFullname("Ada Lovelace")
	a.c.password, a.c.passwordConfirm = "correct horse battery staple", "correct horse battery staple"
	a.c.hostname = "frost01"
}

// recordEvents runs the demo install for the current choices and returns
// every event it emits.
func recordEvents(t *testing.T, a *app) []progress.Event {
	t.Helper()
	data, err := reviewBytes(a.c, a.machine.env.SecureBoot, a.session)
	if err != nil {
		t.Fatal(err)
	}
	l, err := recipe.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var events []progress.Event
	emit := progress.EmitterFunc(func(e progress.Event) error {
		events = append(events, e)
		return nil
	})
	if err := a.be.Install(context.Background(), l, emit); err != nil {
		t.Fatal(err)
	}
	return events
}

// snapshot renders the app's current state to dir/name.png when dir is set.
func snapshot(t *testing.T, a *app, dir, name string, dark bool, before func(tt *ui.Tester)) *ui.Tester {
	t.Helper()
	return snapshotSized(t, a, dir, name, dark, 760, before)
}

// snapshotSized renders at a given window height, to see a long page whole.
func snapshotSized(t *testing.T, a *app, dir, name string, dark bool, height int, before func(tt *ui.Tester)) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.view, 1120, height)
	tt.SetDark(dark)
	if before != nil {
		before(tt)
	}
	tt.Frame()
	tt.Advance(time.Second) // let color transitions from the first frame settle
	if dir == "" {
		return tt
	}
	suffix := "-light"
	if dark {
		suffix = "-dark"
	}
	f, err := os.Create(filepath.Join(dir, name+suffix+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
	return tt
}
