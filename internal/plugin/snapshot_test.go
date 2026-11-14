package plugin

// TestSnapshots locks the plugin's behaviour at its RPC boundary before the
// provider plugins move onto a shared core. Every fixture in
// testdata/snapshots is replayed through New + Service.Call (the path CPA
// takes through main.go) against a fake CPA host and a scripted httptest
// upstream, and each step's output is compared with the fixture: every
// upstream HTTP request (all headers as received, exact body), every host
// callback (method + request) and the raw RPC response envelope.
//
// Fixtures are the source of truth. `go test ./internal/plugin -run
// TestSnapshots -update` rewrites only steps[].output; cases in
// snapshotCases() without a fixture file are written from the table first.
//
// Setup: the service is built by snapshotNewService (snapshot_seams_test.go),
// setup.credentials are stored as auth files in the fake host (auth_index
// idx-1, idx-2, ... in order) and plugin.register is called with setup.config
// as config_yaml (JSON is valid YAML) and schema_version
// pluginabi.SchemaVersion. Setup activity is not recorded.
//
// Placeholders in inputs (config, credential fields, step requests, also
// inside base64 []byte fields): {{UPSTREAM}} is the httptest origin,
// {{UPSTREAM_ADDR}} its host:port and {{DATA_DIR}} the per-run data dir.
//
// Upstream script: replies are matched in order per method+path (query
// ignored); an unmatched request fails the case, and so does a reply that is
// never used. The catalog loads model details (POST /api/show) four at a
// time, so consecutive /api/show replies in a script must be identical.
//
// Normalisation rules (recorded outputs only):
//   - The upstream origin http://127.0.0.1:PORT becomes {{UPSTREAM}}, the
//     bare 127.0.0.1:PORT (an egress check's "source") {{UPSTREAM_ADDR}} and
//     the data dir {{DATA_DIR}}, in every string.
//   - Request log entries (objects with "duration_ms"): "time",
//     "duration_ms", "ttft_ms" and "last_frame_ms" become "<volatile>";
//     "output_tokens_per_second" and "e2e_tokens_per_second" are dropped,
//     since they only exist when a call took more than 50 ms of wall time.
//     Elsewhere (the log summary medians) those two become "<volatile>".
//   - Proxy list entries (objects under "proxy" / "proxies"): the generated
//     "id" and "created_at" become "<volatile>", as does every "proxy_id"
//     and an egress check's "latency_ms".
//   - Base64 []byte fields (Body, Payload, payload, StorageJSON,
//     storage_json, OriginalRequest, RawJSON, config_yaml) are decoded,
//     normalised by the same rules and re-encoded; untouched values keep
//     their exact bytes.
//   - Upstream requests: a run of consecutive POST /api/show requests is
//     sorted by body (they arrive concurrently).
//
// Pinned instead of normalised (snapshot_seams_test.go): the service clock,
// the background catalog refresh loop (never started) and the egress check
// URLs (the fake upstream's /json). Async work is awaited deterministically:
// after each step the runner waits for the stream pumps to finish.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var updateSnapshots = flag.Bool("update", false, "rewrite steps[].output of the RPC snapshot fixtures")

const (
	snapshotDir      = "testdata/snapshots"
	snapshotVolatile = `"<volatile>"`
)

// Fixture format, shared by the four provider plugins. Field order is the
// key order of the files.

type snapshotFixture struct {
	Case           string          `json:"case"`
	Description    string          `json:"description"`
	Setup          snapshotSetup   `json:"setup"`
	UpstreamScript []snapshotReply `json:"upstream_script"`
	Steps          []snapshotStep  `json:"steps"`
}

type snapshotSetup struct {
	Version     string               `json:"version"`
	Config      json.RawMessage      `json:"config"`
	Credentials []snapshotCredential `json:"credentials"`
}

type snapshotCredential struct {
	Name   string          `json:"name"`
	Fields json.RawMessage `json:"fields"`
}

type snapshotReply struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
	Chunks []string    `json:"chunks"`
}

type snapshotStep struct {
	RPC    snapshotRPC     `json:"rpc"`
	Output *snapshotOutput `json:"output,omitempty"`
}

type snapshotRPC struct {
	Method  string          `json:"method"`
	Request json.RawMessage `json:"request"`
}

type snapshotOutput struct {
	UpstreamRequests []snapshotUpstreamRequest `json:"upstream_requests"`
	HostCalls        []snapshotHostCall        `json:"host_calls"`
	Response         json.RawMessage           `json:"response"`
}

type snapshotUpstreamRequest struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Query  string      `json:"query"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

type snapshotHostCall struct {
	Method  string          `json:"method"`
	Request json.RawMessage `json:"request"`
}

// snapshotLifecycleRequest is CPA's plugin.register request.
type snapshotLifecycleRequest struct {
	ConfigYAML         []byte `json:"config_yaml"`
	SchemaVersion      uint32 `json:"schema_version"`
	MultiAuthProviders bool   `json:"multi_auth_providers,omitempty"`
}

func TestSnapshots(t *testing.T) {
	files, errGlob := filepath.Glob(filepath.Join(snapshotDir, "*.json"))
	if errGlob != nil {
		t.Fatal(errGlob)
	}
	onDisk := make(map[string]bool, len(files))
	for _, file := range files {
		onDisk[strings.TrimSuffix(filepath.Base(file), ".json")] = true
	}
	seen := map[string]bool{}
	for _, fixture := range snapshotCases() {
		if seen[fixture.Case] {
			t.Fatalf("case %s is defined twice", fixture.Case)
		}
		seen[fixture.Case] = true
		if onDisk[fixture.Case] {
			continue
		}
		if !*updateSnapshots {
			t.Errorf("fixture %s.json is missing; run go test ./internal/plugin -run TestSnapshots -update", fixture.Case)
			continue
		}
		path := filepath.Join(snapshotDir, fixture.Case+".json")
		if errWrite := writeSnapshotFixture(path, fixture); errWrite != nil {
			t.Fatal(errWrite)
		}
		files = append(files, path)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no snapshot fixtures")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		t.Run(name, func(t *testing.T) { runSnapshotFixture(t, file) })
	}
}

func runSnapshotFixture(t *testing.T, path string) {
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	var fixture snapshotFixture
	if errUnmarshal := json.Unmarshal(raw, &fixture); errUnmarshal != nil {
		t.Fatalf("decode %s: %v", path, errUnmarshal)
	}
	if want := strings.TrimSuffix(filepath.Base(path), ".json"); fixture.Case != want {
		t.Fatalf("fixture case %q does not match its file name %q", fixture.Case, want)
	}

	upstream := newSnapshotUpstream(fixture.UpstreamScript)
	t.Cleanup(upstream.Close)
	dataDir := t.TempDir()
	addr := strings.TrimPrefix(upstream.URL, "http://")
	toReal := snapshotRewriter{text: strings.NewReplacer("{{UPSTREAM}}", upstream.URL, "{{UPSTREAM_ADDR}}", addr, "{{DATA_DIR}}", dataDir).Replace}
	normaliser := snapshotRewriter{text: strings.NewReplacer(upstream.URL, "{{UPSTREAM}}", addr, "{{UPSTREAM_ADDR}}", dataDir, "{{DATA_DIR}}").Replace, normalise: true}

	host := &snapshotHost{}
	svc := snapshotNewService(fixture.Setup.Version, host.call)
	host.svc = svc
	t.Cleanup(svc.Close)
	snapshotSetEgressCheckURLs(svc, []string{upstream.URL + "/json"})

	for _, cred := range fixture.Setup.Credentials {
		fields := snapshotCompact(t, toReal.apply(cred.Fields))
		if errSeed := host.seed(cred.Name, fields); errSeed != nil {
			t.Fatalf("seed %s: %v", cred.Name, errSeed)
		}
	}
	config := snapshotCompact(t, toReal.apply(fixture.Setup.Config))
	register, _ := json.Marshal(snapshotLifecycleRequest{ConfigYAML: config, SchemaVersion: pluginabi.SchemaVersion, MultiAuthProviders: true})
	if errRegister := snapshotDecodeResult(svc.Call(pluginabi.MethodPluginRegister, register), nil); errRegister != nil {
		t.Fatalf("plugin.register: %v", errRegister)
	}
	snapshotWaitIdle(svc)
	host.take()
	if setup := upstream.take(); len(setup) > 0 {
		t.Fatalf("setup made %d upstream requests", len(setup))
	}

	changed := false
	for i, step := range fixture.Steps {
		request := toReal.apply(step.RPC.Request)
		response := svc.Call(step.RPC.Method, request)
		snapshotWaitIdle(svc)
		got := &snapshotOutput{
			UpstreamRequests: normaliser.upstreamRequests(upstream.take()),
			HostCalls:        normaliser.hostCalls(host.take()),
			Response:         normaliser.apply(response),
		}
		if *updateSnapshots {
			fixture.Steps[i].Output = got
			changed = true
			continue
		}
		if step.Output == nil {
			t.Errorf("step %d (%s) has no recorded output; run with -update", i, step.RPC.Method)
			continue
		}
		want, errWant := snapshotEncode(step.Output)
		have, errHave := snapshotEncode(got)
		if errWant != nil || errHave != nil {
			t.Fatalf("encode step %d: %v %v", i, errWant, errHave)
		}
		if !bytes.Equal(want, have) {
			t.Errorf("step %d (%s) output changed\n--- want\n%s\n--- got\n%s", i, step.RPC.Method, want, have)
		}
	}
	for _, problem := range upstream.problems() {
		t.Error(problem)
	}
	if *updateSnapshots && changed && !t.Failed() {
		if errWrite := writeSnapshotFixture(path, fixture); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
}

func writeSnapshotFixture(path string, fixture snapshotFixture) error {
	raw, errEncode := snapshotEncode(fixture)
	if errEncode != nil {
		return errEncode
	}
	if errDir := os.MkdirAll(filepath.Dir(path), 0o755); errDir != nil {
		return errDir
	}
	return os.WriteFile(path, raw, 0o644)
}

// snapshotEncode is the fixture encoding: two-space indent, no HTML escaping.
func snapshotEncode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if errEncode := enc.Encode(v); errEncode != nil {
		return nil, errEncode
	}
	return buf.Bytes(), nil
}

func snapshotCompact(t *testing.T, raw []byte) []byte {
	t.Helper()
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}")
	}
	var buf bytes.Buffer
	if errCompact := json.Compact(&buf, raw); errCompact != nil {
		t.Fatalf("compact %s: %v", raw, errCompact)
	}
	return buf.Bytes()
}

// snapshotDecodeResult decodes a plugin or host envelope's result into out
// (when non-nil) and turns a failed envelope into an error.
func snapshotDecodeResult(raw []byte, out any) error {
	var envelope pluginabi.Envelope
	if errUnmarshal := json.Unmarshal(raw, &envelope); errUnmarshal != nil {
		return fmt.Errorf("decode envelope %s: %w", raw, errUnmarshal)
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)
		}
		return fmt.Errorf("envelope not ok")
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

func snapshotOK(result any) []byte {
	rawResult, _ := json.Marshal(result)
	raw, _ := json.Marshal(pluginabi.Envelope{OK: true, Result: rawResult})
	return raw
}

func snapshotFail(message string) []byte {
	raw, _ := json.Marshal(pluginabi.Envelope{OK: false, Error: &pluginabi.Error{Code: "host_error", Message: message}})
	return raw
}

// ---------------------------------------------------------------------------
// Normalisation

// snapshotBase64Keys are the []byte fields of the RPC types, which travel as
// base64 strings.
var snapshotBase64Keys = map[string]bool{
	"Body": true, "Payload": true, "payload": true, "StorageJSON": true, "storage_json": true,
	"OriginalRequest": true, "RawJSON": true, "config_yaml": true,
}

// snapshotRewriter rewrites JSON while keeping member order and the exact
// bytes of every value it leaves alone. text is applied to every string;
// normalise also applies the volatile-field rules.
type snapshotRewriter struct {
	text      func(string) string
	normalise bool
}

func (w snapshotRewriter) apply(raw []byte) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw
	}
	if !json.Valid(raw) {
		return snapshotString(w.text(string(raw)))
	}
	out, _ := w.rewrite(raw, "")
	return append(json.RawMessage(nil), out...)
}

func (w snapshotRewriter) rewrite(raw []byte, context string) ([]byte, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}
	switch trimmed[0] {
	case '{':
		return w.object(trimmed, context)
	case '[':
		return w.array(trimmed, context)
	case '"':
		var text string
		if json.Unmarshal(trimmed, &text) != nil {
			return raw, false
		}
		if replaced := w.text(text); replaced != text {
			return snapshotString(replaced), true
		}
	}
	return raw, false
}

type snapshotMember struct {
	key   string
	value []byte
}

func (w snapshotRewriter) object(raw []byte, context string) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if _, errToken := dec.Token(); errToken != nil {
		return raw, false
	}
	var members []snapshotMember
	logEntry := false
	for dec.More() {
		token, errToken := dec.Token()
		if errToken != nil {
			return raw, false
		}
		key, _ := token.(string)
		var value json.RawMessage
		if errDecode := dec.Decode(&value); errDecode != nil {
			return raw, false
		}
		members = append(members, snapshotMember{key: key, value: value})
		if key == "duration_ms" {
			logEntry = true
		}
	}
	changed := false
	out := make([]snapshotMember, 0, len(members))
	for _, member := range members {
		value, memberChanged, keep := w.member(member, context, logEntry)
		changed = changed || memberChanged
		if keep {
			out = append(out, snapshotMember{key: member.key, value: value})
		}
	}
	if !changed {
		return raw, false
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, member := range out {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(snapshotString(member.key))
		buf.WriteByte(':')
		buf.Write(member.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), true
}

func (w snapshotRewriter) member(member snapshotMember, context string, logEntry bool) (value []byte, changed, keep bool) {
	if w.normalise {
		if logEntry {
			switch member.key {
			case "output_tokens_per_second", "e2e_tokens_per_second":
				return nil, true, false
			case "time", "duration_ms", "ttft_ms", "last_frame_ms":
				return []byte(snapshotVolatile), true, true
			}
		}
		switch member.key {
		case "output_tokens_per_second", "e2e_tokens_per_second", "latency_ms", "proxy_id":
			return []byte(snapshotVolatile), true, true
		}
		if (context == "proxy" || context == "proxies") && (member.key == "id" || member.key == "created_at") {
			return []byte(snapshotVolatile), true, true
		}
	}
	if snapshotBase64Keys[member.key] {
		if value, changed, ok := w.base64(member.value); ok {
			return value, changed, true
		}
	}
	value, changed = w.rewrite(member.value, member.key)
	return value, changed, true
}

func (w snapshotRewriter) array(raw []byte, context string) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if _, errToken := dec.Token(); errToken != nil {
		return raw, false
	}
	var items [][]byte
	changed := false
	for dec.More() {
		var item json.RawMessage
		if errDecode := dec.Decode(&item); errDecode != nil {
			return raw, false
		}
		next, itemChanged := w.rewrite(item, context)
		changed = changed || itemChanged
		items = append(items, next)
	}
	if !changed {
		return raw, false
	}
	return append(append([]byte{'['}, bytes.Join(items, []byte{','})...), ']'), true
}

// base64 rewrites a base64 []byte field; ok is false when the value is not
// a base64 string.
func (w snapshotRewriter) base64(raw []byte) (value []byte, changed, ok bool) {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return nil, false, false
	}
	decoded, errDecode := base64.StdEncoding.DecodeString(encoded)
	if errDecode != nil {
		return nil, false, false
	}
	var next []byte
	if json.Valid(decoded) {
		next, changed = w.rewrite(decoded, "")
	} else {
		text := w.text(string(decoded))
		next, changed = []byte(text), text != string(decoded)
	}
	if !changed {
		return raw, false, true
	}
	return snapshotString(base64.StdEncoding.EncodeToString(next)), true, true
}

func (w snapshotRewriter) upstreamRequests(requests []snapshotUpstreamRequest) []snapshotUpstreamRequest {
	out := make([]snapshotUpstreamRequest, 0, len(requests))
	for _, request := range requests {
		header := http.Header{}
		for key, values := range request.Header {
			for _, value := range values {
				header[key] = append(header[key], w.text(value))
			}
		}
		out = append(out, snapshotUpstreamRequest{
			Method: request.Method,
			Path:   request.Path,
			Query:  w.text(request.Query),
			Header: header,
			Body:   w.text(request.Body),
		})
	}
	// Model details are fetched concurrently; order a run of them by body.
	for i := 0; i < len(out); {
		j := i
		for j < len(out) && out[j].Method == http.MethodPost && out[j].Path == "/api/show" {
			j++
		}
		if j-i > 1 {
			run := out[i:j]
			sort.SliceStable(run, func(a, b int) bool { return run[a].Body < run[b].Body })
		}
		i = max(j, i+1)
	}
	return out
}

func (w snapshotRewriter) hostCalls(calls []snapshotHostCall) []snapshotHostCall {
	out := make([]snapshotHostCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, snapshotHostCall{Method: call.Method, Request: w.apply(call.Request)})
	}
	return out
}

// snapshotString encodes a JSON string without HTML escaping.
func snapshotString(text string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(text)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// ---------------------------------------------------------------------------
// Fake upstream

// snapshotUpstream serves the fixture's upstream script and records every
// request it receives.
type snapshotUpstream struct {
	*httptest.Server

	mu        sync.Mutex
	script    []snapshotReply
	used      []bool
	requests  []snapshotUpstreamRequest
	unmatched []string
}

func newSnapshotUpstream(script []snapshotReply) *snapshotUpstream {
	u := &snapshotUpstream{script: script, used: make([]bool, len(script))}
	u.Server = httptest.NewServer(http.HandlerFunc(u.serve))
	return u
}

func (u *snapshotUpstream) serve(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.requests = append(u.requests, snapshotUpstreamRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   string(body),
	})
	index := -1
	for i, reply := range u.script {
		if !u.used[i] && reply.Method == r.Method && reply.Path == r.URL.Path {
			index = i
			u.used[i] = true
			break
		}
	}
	if index < 0 {
		u.unmatched = append(u.unmatched, r.Method+" "+r.URL.RequestURI())
	}
	u.mu.Unlock()
	if index < 0 {
		http.Error(rw, "snapshot: no scripted reply for "+r.Method+" "+r.URL.Path, http.StatusTeapot)
		return
	}
	reply := u.script[index]
	for key, values := range reply.Header {
		rw.Header()[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	rw.WriteHeader(status)
	if len(reply.Chunks) == 0 {
		_, _ = io.WriteString(rw, reply.Body)
		return
	}
	flusher, _ := rw.(http.Flusher)
	for _, chunk := range reply.Chunks {
		_, _ = io.WriteString(rw, chunk)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (u *snapshotUpstream) take() []snapshotUpstreamRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := u.requests
	u.requests = nil
	return out
}

func (u *snapshotUpstream) problems() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []string
	for _, request := range u.unmatched {
		out = append(out, "unmatched upstream request: "+request)
	}
	for i, used := range u.used {
		if !used {
			out = append(out, fmt.Sprintf("upstream_script[%d] (%s %s) was never requested", i, u.script[i].Method, u.script[i].Path))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Fake CPA host

// snapshotAuthFile is one auth file held by snapshotHost.
type snapshotAuthFile struct {
	index    string
	name     string
	provider string
	label    string
	disabled bool
	raw      []byte
	// models are the IDs CPA registered for the file (after a save).
	models []string
}

// snapshotHost is an in-memory stand-in for CPA's host callbacks, adapted
// from the wokey plugin's fakeHost. It records every callback. Saving an
// auth file goes back into the plugin through auth.parse and model.for_auth
// (RPC calls, not recorded) and registers the models with CPA's prefix rules.
type snapshotHost struct {
	svc *Service

	mu    sync.Mutex
	files []*snapshotAuthFile
	next  int
	calls []snapshotHostCall
}

const snapshotAuthDir = "/auths"

// snapshotHostSummary is the host config summary the fake host sends.
var snapshotHostSummary = pluginapi.HostConfigSummary{AuthDir: snapshotAuthDir}

// snapshotAuthList is the host.auth.list result.
type snapshotAuthList struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

func (h *snapshotHost) seed(name string, raw []byte) error {
	var fields map[string]any
	if errUnmarshal := json.Unmarshal(raw, &fields); errUnmarshal != nil {
		return errUnmarshal
	}
	label, _ := fields["label"].(string)
	if strings.TrimSpace(label) == "" {
		label = strings.TrimSuffix(name, filepath.Ext(name))
	}
	disabled, _ := fields["disabled"].(bool)
	provider, _ := fields["type"].(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	h.files = append(h.files, &snapshotAuthFile{
		index:    fmt.Sprintf("idx-%d", h.next),
		name:     name,
		provider: strings.ToLower(strings.TrimSpace(provider)),
		label:    strings.TrimSpace(label),
		disabled: disabled,
		raw:      append([]byte(nil), raw...),
		models:   []string{},
	})
	return nil
}

func (h *snapshotHost) take() []snapshotHostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.calls
	h.calls = nil
	return out
}

func (h *snapshotHost) call(method string, request []byte) ([]byte, error) {
	recorded := json.RawMessage(append([]byte(nil), request...))
	if !json.Valid(request) {
		recorded = snapshotString(string(request))
	}
	h.mu.Lock()
	h.calls = append(h.calls, snapshotHostCall{Method: method, Request: recorded})
	h.mu.Unlock()

	switch method {
	case pluginabi.MethodHostLog, pluginabi.MethodHostStreamEmit, pluginabi.MethodHostStreamClose:
		return snapshotOK(struct{}{}), nil
	case pluginabi.MethodHostAuthList:
		h.mu.Lock()
		defer h.mu.Unlock()
		list := snapshotAuthList{Files: make([]pluginapi.HostAuthFileEntry, 0, len(h.files))}
		for _, file := range h.files {
			list.Files = append(list.Files, pluginapi.HostAuthFileEntry{
				ID:        file.name,
				AuthIndex: file.index,
				Name:      file.name,
				Type:      file.provider,
				Provider:  file.provider,
				Label:     file.label,
				Disabled:  file.disabled,
				Source:    "file",
				Path:      snapshotAuthDir + "/" + file.name,
			})
		}
		return snapshotOK(list), nil
	case pluginabi.MethodHostAuthGet:
		var req pluginapi.HostAuthGetRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return snapshotFail(errUnmarshal.Error()), nil
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, file := range h.files {
			if file.index == req.AuthIndex {
				return snapshotOK(pluginapi.HostAuthGetResponse{AuthIndex: file.index, Name: file.name, Path: snapshotAuthDir + "/" + file.name, JSON: file.raw}), nil
			}
		}
		return snapshotFail("auth not found"), nil
	case pluginabi.MethodHostAuthSave:
		var req pluginapi.HostAuthSaveRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return snapshotFail(errUnmarshal.Error()), nil
		}
		return h.save(req), nil
	case pluginabi.MethodHostAuthModels:
		h.mu.Lock()
		defer h.mu.Unlock()
		var out pluginapi.HostAuthModelsResponse
		out.Credentials = []pluginapi.HostAuthModels{}
		for _, file := range h.files {
			if file.disabled {
				continue
			}
			out.Credentials = append(out.Credentials, pluginapi.HostAuthModels{ID: file.name, AuthIndex: file.index, Provider: file.provider, Models: append([]string{}, file.models...)})
		}
		return snapshotOK(out), nil
	default:
		return snapshotFail("unexpected host method " + method), nil
	}
}

// save mimics host.auth.save: CPA writes the file, parses it through the
// plugin and asks the plugin for its models. The lock is not held while
// calling into the plugin, which calls back into the host.
func (h *snapshotHost) save(req pluginapi.HostAuthSaveRequest) []byte {
	name := filepath.Base(req.Name)
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		return snapshotFail("auth file name must end with .json")
	}
	parseRequest, _ := json.Marshal(pluginapi.AuthParseRequest{Provider: "ollama", Path: snapshotAuthDir + "/" + name, FileName: name, RawJSON: req.JSON, Host: snapshotHostSummary})
	var parsed pluginapi.AuthParseResponse
	if errParse := snapshotDecodeResult(h.svc.Call(pluginabi.MethodAuthParse, parseRequest), &parsed); errParse != nil {
		return snapshotFail(errParse.Error())
	}
	if !parsed.Handled {
		return snapshotFail("no provider handled the auth file")
	}
	models := []string{}
	if !parsed.Auth.Disabled {
		modelRequest, _ := json.Marshal(snapshotAuthModelRequest{AuthModelRequest: pluginapi.AuthModelRequest{
			AuthID:       name,
			AuthProvider: "ollama",
			StorageJSON:  req.JSON,
			Metadata:     parsed.Auth.Metadata,
			Host:         snapshotHostSummary,
		}})
		var resp pluginapi.ModelResponse
		if errModels := snapshotDecodeResult(h.svc.Call(pluginabi.MethodModelForAuth, modelRequest), &resp); errModels != nil {
			return snapshotFail(errModels.Error())
		}
		prefix := strings.TrimSpace(parsed.Auth.Prefix)
		for _, model := range resp.Models {
			if prefix == "" {
				models = append(models, model.ID)
				continue
			}
			// Without routing.force-model-prefix CPA registers both IDs.
			models = append(models, model.ID, prefix+"/"+model.ID)
		}
		sort.Strings(models)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	saved := pluginapi.HostAuthSaveResponse{Name: name, Path: snapshotAuthDir + "/" + name}
	for _, file := range h.files {
		if file.name == name {
			file.raw = append([]byte(nil), req.JSON...)
			file.label = parsed.Auth.Label
			file.disabled = parsed.Auth.Disabled
			file.models = models
			return snapshotOK(saved)
		}
	}
	h.next++
	h.files = append(h.files, &snapshotAuthFile{
		index:    fmt.Sprintf("idx-%d", h.next),
		name:     name,
		provider: "ollama",
		label:    parsed.Auth.Label,
		disabled: parsed.Auth.Disabled,
		raw:      append([]byte(nil), req.JSON...),
		models:   models,
	})
	return snapshotOK(saved)
}

// CPA's RPC request shapes (internal/pluginhost/rpc_schema.go in CPA v8).

type snapshotExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type snapshotAuthModelRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type snapshotAuthRefreshRequest struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type snapshotManagementRequest struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type snapshotQuotaFetchRequest struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type snapshotInterceptRequest struct {
	pluginapi.RequestInterceptRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// snapshotQuery builds url.Values from key/value pairs.
func snapshotQuery(pairs ...string) url.Values {
	values := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		values.Add(pairs[i], pairs[i+1])
	}
	return values
}
