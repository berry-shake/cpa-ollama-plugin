package plugin

import (
	"testing"
	"time"
)

// Snapshot seams: the only place the RPC snapshot tests reach into the
// Service's unexported state. Everything else drives the plugin through
// Service.Call and the host callbacks.

// snapshotProviderID is the auth provider the fake host parses files for.
const snapshotProviderID = ProviderID

// snapshotConcurrentPaths are the upstream routes the plugin calls
// concurrently: the catalog loads model details four at a time.
var snapshotConcurrentPaths = []string{"POST /api/show"}

// snapshotNewService builds the service the way main.go does, then pins the
// clock, keeps plugin.register from starting the catalog refresh loop and
// points the egress check at the fake upstream.
func snapshotNewService(version string, host HostCaller, egressCheckURLs []string) *Service {
	svc := New(version, host)
	svc.now = func() time.Time { return snapshotClock }
	svc.loopOnce.Do(func() {})
	svc.egressCheckURLs = append([]string(nil), egressCheckURLs...)
	return svc
}

// snapshotDropQueuedRefresh discards a queued catalog refresh without
// running it.
func snapshotDropQueuedRefresh(svc *Service) {
	select {
	case <-svc.kick:
	default:
	}
}

// snapshotRunQueuedRefresh runs, synchronously, the refresh the background
// loop would run for a queued kick, and reports whether one was queued.
func snapshotRunQueuedRefresh(svc *Service) bool {
	select {
	case <-svc.kick:
		_, _ = svc.refreshCatalog(false)
		return true
	default:
		return false
	}
}

// snapshotWaitIdle blocks until every background stream pump has closed its
// host stream and written its request log entry.
func snapshotWaitIdle(svc *Service) {
	svc.streams.Wait()
}

// snapshotSkeletons are the generated inputs of the cases.
func snapshotSkeletons(t *testing.T) []snapshotFixture {
	return snapshotCases(t)
}
