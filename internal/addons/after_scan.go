package addons

import (
	"context"
	"encoding/json"
)

// AfterScan tells the addons that declare library.afterScan that a library
// scan is done, once per scan (library.Jobs.AfterScan). It returns at once:
// the call runs on a goroutine of its own (Each: every such addon that is
// on and running, in parallel, body {}, under the hook's 60 s), so a scan
// never waits for an addon. An addon that is down or not running is left
// out quietly, one that is slow or fails is logged and left out; nothing
// is retried, since the next scan calls again. A call may still be running
// when the next scan's starts.
func (h *Hooks) AfterScan(ctx context.Context) {
	go h.afterScan(ctx)
}

// afterScan is AfterScan's call, made to its end.
func (h *Hooks) afterScan(ctx context.Context) {
	h.Each(ctx, "library.afterScan", nil, nil, func(string, json.RawMessage) {})
}
