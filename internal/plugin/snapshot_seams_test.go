package plugin

import "time"

// The only places the snapshot runner reaches into unexported internals.
// Everything else in the snapshot tests goes through New and Service.Call,
// so after the refactor only this file has to be rewritten.

// snapshotClock is the pinned service clock: status "now", catalog
// fetched_at / checked_at, account fetched_at, egress check checked_at and
// the auth.parse / auth.refresh next refresh time all read it.
var snapshotClock = time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)

// snapshotNewService builds the service the way main.go does, then pins the
// clock and keeps the background catalog refresh loop from starting, so the
// catalog is only loaded by the RPC steps themselves (model.for_auth with an
// empty catalog, the models view and the models refresh route all run the
// same refresh code the loop runs).
func snapshotNewService(version string, host HostCaller) *Service {
	svc := New(version, host)
	svc.now = func() time.Time { return snapshotClock }
	svc.loopOnce.Do(func() {})
	return svc
}

// snapshotSetEgressCheckURLs points the proxy egress check at the fake
// upstream instead of ipinfo.io / api.ip.sb.
func snapshotSetEgressCheckURLs(svc *Service, urls []string) {
	svc.egressCheckURLs = append([]string(nil), urls...)
}

// snapshotWaitIdle waits for the background stream pumps started by
// executor.execute_stream; each one emits, closes the host stream and writes
// its request log row before it is done.
func snapshotWaitIdle(svc *Service) {
	svc.streams.Wait()
}
