package ollama_test

import (
	"testing"
	"time"

	"github.com/berry-shake/cpa-ollama-plugin/internal/ollama"
	"github.com/berry-shake/cpa-provider-core/core"
	"github.com/berry-shake/cpa-provider-core/coretest"
)

// TestSnapshots replays testdata/snapshots through the plugin (see
// cpa-provider-core's coretest package and README).
//
//	go test ./internal/ollama -run TestSnapshots           # compare
//	go test ./internal/ollama -run TestSnapshots -update   # rewrite outputs
func TestSnapshots(t *testing.T) {
	coretest.RunSnapshots(t, coretest.Binding{
		ProviderID: ollama.ProviderID,
		NewService: func(version string, host func(string, []byte) ([]byte, error), upstreamURL string) coretest.Plugin {
			return core.NewWithOptions(ollama.New(), version, host, core.Options{
				Now:             func() time.Time { return coretest.Clock },
				ManualRefresh:   true,
				EgressCheckURLs: []string{upstreamURL + "/json"},
			})
		},
		Cases: snapshotCases,
		// The catalog loads model details four at a time.
		ConcurrentPaths: []string{"POST /api/show"},
	})
}

// The case table (snapshot_cases_test.go) predates coretest; these names
// keep it as it was written.
type (
	snapshotFixture     = coretest.Fixture
	snapshotSetup       = coretest.Setup
	snapshotCredential  = coretest.Credential
	snapshotStep        = coretest.Step
	snapshotRPC         = coretest.RPC
	snapshotReply       = coretest.Reply
	cpaLifecycleRequest = coretest.LifecycleRequest
	cpaAuthModelRequest = coretest.AuthModelRequest
)

const snapshotAuthDir = coretest.AuthDir

var snapshotHostSummary = coretest.HostSummary

var snapshotCaseJSON = coretest.CaseJSON
