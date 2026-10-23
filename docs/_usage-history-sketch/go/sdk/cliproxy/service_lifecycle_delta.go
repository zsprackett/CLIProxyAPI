package cliproxy

// Delta to sdk/cliproxy/service_lifecycle.go.
//
// Run, non-home branch (~L100). quota-forecast is no longer a usage plugin
// (it reads usagehistory instead), so its RegisterNamedPlugin line goes:
//
//	quotaforecast.Default().Start(ctx, s.coreManager)
//	usage.RegisterNamedPlugin("request-log", requestlog.Default())
//	requestlog.Default().Start(ctx, s.coreManager)
//	usage.RegisterNamedPlugin("usage-history", usagehistory.Default())
//	usagehistory.Default().Start(ctx, s.coreManager)
//
// Shutdown (~L238-367). The fix covers the class, not just the new store: every
// usage sink stops AFTER the queue drained, and the queue drains AFTER the HTTP
// server stopped accepting and finished in-flight requests (which publish).
//
//	... home teardown, watcherCancel, StopAutoRefresh            (unchanged)
//	- quotaforecast.Default().Stop(ctx)                           (moved down)
//	- requestlog.Default().Stop(ctx)                              (moved down)
//	  watcher.Stop, wsGateway.Stop, authQueueStop, pprof, discovery (unchanged)
//	  s.server.Stop(ctx)                                           in-flight requests finish and publish
//	+ usage.StopDefault()                                          drains the queue into every plugin, waits
//	+ quotaforecast.Default().Stop(ctx)                            final save
//	+ requestlog.Default().Stop(ctx)                               final snapshot
//	+ usagehistory.Default().Stop(ctx)                             final flush, checkpoint, close
//	  pluginHost teardown                                          now after the drain, so plugin usage sinks get the tail too
//	- usage.StopDefault()                                          (was last, did not wait)
//
// Home mode: usagehistory is never started; Stop is a no-op.
