package plugin

import (
	"testing"
	"time"

	"github.com/berry-shake/cpa-ollama-plugin/internal/requestlog"
)

// A logged call carries both speeds, worked out from the figures it is
// stored with: a stream has the generation speed, every call with output
// the end-to-end speed.
func TestRecordSpeeds(t *testing.T) {
	log := requestlog.New(func(string) {})
	log.Configure("", 100, 0)
	defer log.Close()
	start := time.Now().Add(-3 * time.Second)

	stream := &callRecord{log: log, start: start, entry: requestlog.Entry{Time: start, Stream: true, UpstreamStatus: 200}, first: start.Add(time.Second), last: start.Add(2 * time.Second), frames: 5}
	stream.usage.output = 101
	stream.finish(nil, "")
	buffered := &callRecord{log: log, start: start, entry: requestlog.Entry{Time: start, UpstreamStatus: 200}}
	buffered.usage.output = 100
	buffered.finish(nil, "")

	items := log.Query(requestlog.Query{}).Items
	if len(items) != 2 {
		t.Fatalf("logged %d entries", len(items))
	}
	b, s := items[0], items[1]
	if s.TTFTMS == nil || s.TokensPerSecond == nil || s.E2ETokensPerSecond == nil {
		t.Fatalf("stream entry = %+v", s)
	}
	if want := requestlog.GenerationSpeed(true, 101, s.DurationMS, s.TTFTMS); *s.TokensPerSecond != *want {
		t.Fatalf("generation speed = %v, want %v", *s.TokensPerSecond, *want)
	}
	if want := requestlog.E2ESpeed(101, s.DurationMS); *s.E2ETokensPerSecond != *want {
		t.Fatalf("end-to-end speed = %v, want %v", *s.E2ETokensPerSecond, *want)
	}
	if b.TTFTMS != nil || b.TokensPerSecond != nil || b.E2ETokensPerSecond == nil || *b.E2ETokensPerSecond != *requestlog.E2ESpeed(100, b.DurationMS) {
		t.Fatalf("buffered entry = %+v", b)
	}
}
