package usage

// Delta to sdk/cliproxy/usage/manager.go: Stop waits for the drain.
//
// Today: Stop() cancels, sets closed, broadcasts, and RETURNS. run() keeps
// draining the queue after closed (it only exits when closed && empty), but
// nobody waits for it, so service_lifecycle.go's final usage.StopDefault()
// races process exit and plugins that already flushed never see the tail.
//
// Change:
//
//	type Manager struct {
//		...
//		done chan struct{} // closed when run returns; nil until Start
//	}
//
//	Start: m.once.Do(func() { ...; m.done = make(chan struct{}); go func() { defer close(m.done); m.run(workerCtx) }() })
//
//	Stop: m.stopOnce.Do(func() { cancel; closed = true; Broadcast })
//	      if done := m.started(); done != nil { <-done }   // wait for the drain
//
// The wait is unbounded on purpose: HandleUsage must not do I/O (package rule),
// so draining is CPU-only (~µs per record per plugin). A plugin that breaks the
// rule and blocks would hang shutdown; the 30s Shutdown context in Run's defer
// is the outer bound, enforced by the process exiting. If that is unacceptable,
// add StopContext(ctx) and have StopDefault call it with the shutdown ctx; the
// lifecycle code below would then read `usage.StopDefaultContext(ctx)`.
//
// After Stop, Publish drops records (closed), unchanged.

// StopDefault stops the default manager and waits until every queued record has
// been delivered to every plugin.
//
//	func StopDefault() { DefaultManager().Stop() }   // signature unchanged, now waits
