package ollama_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// snapshotCases generates the input skeletons of fixtures that do not exist
// yet. Once a fixture is written, the file is the source of truth and this
// table is not read for it again.

const (
	snapVersion = "9.9.9-snapshot"
	// Fake keys; never real secrets.
	snapKeyA   = "ollama-snap-key-aaaaaaaaaaaaaaaa"
	snapKeyB   = "ollama-snap-key-bbbbbbbbbbbbbbbb"
	snapModel  = "gpt-oss:120b"
	mgmtPrefix = "/v0/management/plugins/ollama"
)

// snapCred is one seeded auth file; credentials get auth_index idx-1, idx-2,
// ... in the order a case lists them.
type snapCred struct {
	name   string
	index  string
	fields map[string]any
}

func (c snapCred) raw() []byte {
	raw, _ := json.Marshal(c.fields)
	return raw
}

// metadata is what CPA keeps as the auth's metadata after auth.parse.
func (c snapCred) metadata() map[string]any {
	out := make(map[string]any, len(c.fields)+1)
	for key, value := range c.fields {
		out[key] = value
	}
	out["type"] = "ollama"
	return out
}

func cred(name, index string, fields map[string]any) snapCred {
	return snapCred{name: name, index: index, fields: fields}
}

var (
	credA = cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA})
	credB = cred("k2.json", "idx-2", map[string]any{"type": "ollama", "api_key": snapKeyB})
)

func snapJSON(v any) json.RawMessage {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		panic(errMarshal)
	}
	return raw
}

func snapConfig(extra map[string]any) json.RawMessage {
	config := map[string]any{"base-url": "{{UPSTREAM}}", "data-dir": "{{DATA_DIR}}", "models-refresh-minutes": 0}
	for key, value := range extra {
		config[key] = value
	}
	return snapJSON(config)
}

func snapFixture(name, description string, config json.RawMessage, creds []snapCred, script []snapshotReply, steps ...snapshotStep) snapshotFixture {
	if config == nil {
		config = snapConfig(nil)
	}
	credentials := make([]snapshotCredential, 0, len(creds))
	for _, c := range creds {
		credentials = append(credentials, snapshotCredential{Name: c.name, Fields: c.raw()})
	}
	if script == nil {
		script = []snapshotReply{}
	}
	return snapshotFixture{
		Case:           name,
		Description:    description,
		Setup:          snapshotSetup{Version: snapVersion, Config: config, Credentials: credentials},
		UpstreamScript: script,
		Steps:          steps,
	}
}

func creds(list ...snapCred) []snapCred { return list }

func script(list ...snapshotReply) []snapshotReply { return list }

// ---------------------------------------------------------------------------
// Upstream replies

func reply(method, path string, status int, contentType, body string, header ...string) snapshotReply {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(header); i += 2 {
		h.Add(header[i], header[i+1])
	}
	return snapshotReply{Method: method, Path: path, Status: status, Header: h, Body: body, Chunks: []string{}}
}

func replyJSON(method, path string, status int, body string, header ...string) snapshotReply {
	return reply(method, path, status, "application/json; charset=utf-8", body, header...)
}

func replySSE(path string, chunks ...string) snapshotReply {
	return snapshotReply{Method: http.MethodPost, Path: path, Status: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"up-stream-1"}}, Body: "", Chunks: chunks}
}

func sse(event, data string) string {
	if event == "" {
		return "data: " + data + "\n\n"
	}
	return "event: " + event + "\ndata: " + data + "\n\n"
}

// Canned Ollama answers.
const (
	chatPayload   = `{"model":"gpt-oss:120b","messages":[{"role":"user","content":"Hi"}],"reasoning_effort":"high","temperature":0.2}`
	chatResponse  = `{"id":"chatcmpl-snap1","object":"chat.completion","created":1763107200,"model":"gpt-oss:120b","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!","reasoning":"thinking"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17,"completion_tokens_details":{"reasoning_tokens":2}}}`
	respPayload   = `{"model":"gpt-oss:120b","input":"Hi","reasoning":{"effort":"low"},"store":false}`
	respResponse  = `{"id":"resp_snap1","object":"response","created_at":1763107200,"status":"completed","model":"gpt-oss:120b","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":6,"total_tokens":16,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}`
	msgPayload    = `{"model":"gpt-oss:120b","max_tokens":256,"messages":[{"role":"user","content":"Hi"}],"thinking":{"type":"enabled","budget_tokens":1024}}`
	msgResponse   = `{"id":"msg_snap1","type":"message","role":"assistant","model":"gpt-oss:120b","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":7,"cache_read_input_tokens":5,"cache_creation_input_tokens":1}}`
	chatChunkHead = `{"id":"chatcmpl-snap2","object":"chat.completion.chunk","created":1763107200,"model":"gpt-oss:120b",`
)

func chatChunk(rest string) string { return sse("", chatChunkHead+rest) }

func chatStreamChunks() []string {
	return []string{
		": keepalive\n\n",
		chatChunk(`"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`),
		chatChunk(`"choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`),
		chatChunk(`"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`),
		chatChunk(`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		chatChunk(`"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14}}`),
		"data: [DONE]\n\n",
	}
}

func responsesStreamChunks() []string {
	return []string{
		sse("response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"resp_snap2","object":"response","created_at":1763107200,"status":"in_progress","model":"gpt-oss:120b","output":[]}}`),
		sse("response.output_item.added", `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message","id":"msg_2","status":"in_progress","role":"assistant","content":[]}}`),
		sse("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_2","output_index":0,"content_index":0,"delta":"Hello"}`),
		sse("response.completed", `{"type":"response.completed","sequence_number":3,"response":{"id":"resp_snap2","object":"response","created_at":1763107200,"status":"completed","model":"gpt-oss:120b","output":[{"type":"message","id":"msg_2","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`),
	}
}

func messagesStreamChunks() []string {
	return []string{
		sse("message_start", `{"type":"message_start","message":{"id":"msg_snap2","type":"message","role":"assistant","model":"gpt-oss:120b","content":[],"stop_reason":null,"usage":{"input_tokens":20,"output_tokens":1}}}`),
		sse("ping", `{"type":"ping"}`),
		sse("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sse("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`),
		sse("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}`),
		sse("message_stop", `{"type":"message_stop"}`),
	}
}

// Catalog answers. Consecutive /api/show replies must be identical (the
// details are fetched concurrently).
const (
	tagsThree    = `{"models":[{"name":"gpt-oss:120b","model":"gpt-oss:120b","modified_at":"2025-08-05T12:00:00Z","size":65369818941,"digest":"aaaa1111","capabilities":["completion","tools","thinking"]},{"name":"qwen3-vl:235b","model":"qwen3-vl:235b","modified_at":"2025-10-01T00:00:00Z","size":125000000000,"digest":"bbbb2222","capabilities":["completion","vision"]},{"name":"embed-snap:1b","model":"embed-snap:1b","modified_at":"2025-01-02T03:04:05Z","size":1000000,"digest":"cccc3333","capabilities":["embedding"]}]}`
	showShared   = `{"capabilities":["completion","tools"],"details":{"family":"snapfam","parameter_size":"116.8B","quantization_level":"MXFP4"},"model_info":{"general.architecture":"snaparch","snaparch.context_length":131072},"thinking":{"values":["low","medium","high"],"default":"medium"}}`
	tagsOne      = `{"models":[{"name":"gpt-oss:120b","model":"gpt-oss:120b","modified_at":"2025-08-05T12:00:00Z","size":65369818941,"digest":"aaaa1111"}]}`
	showLevels   = `{"capabilities":["completion","tools","thinking"],"details":{"family":"gptoss","parameter_size":"116829156672","quantization_level":"MXFP4"},"model_info":{"general.architecture":"gptoss","gptoss.context_length":131072},"thinking":{"values":["low","medium","high"],"default":"medium"}}`
	showToggle   = `{"capabilities":["completion","thinking","vision"],"details":{"family":"qwen3","parameter_size":"8.0B","quantization_level":"Q4_K_M"},"model_info":{"qwen3.context_length":40960},"thinking":{"values":[true,false],"default":false}}`
	showNoDetail = `{"capabilities":["completion"]}`
)

func catalogThree() []snapshotReply {
	return []snapshotReply{
		replyJSON(http.MethodGet, "/api/tags", 200, tagsThree),
		replyJSON(http.MethodPost, "/api/show", 200, showShared),
		replyJSON(http.MethodPost, "/api/show", 200, showShared),
		replyJSON(http.MethodPost, "/api/show", 200, showShared),
	}
}

func catalogOne(show string) []snapshotReply {
	return []snapshotReply{
		replyJSON(http.MethodGet, "/api/tags", 200, tagsOne),
		replyJSON(http.MethodPost, "/api/show", 200, show),
	}
}

// Account answers.
const (
	balanceIncluded = `{"included":{"balance_usd":12.5,"allowance_usd":20,"period":{"from":"2026-11-01T00:00:00Z","until":"2026-12-01T00:00:00Z"}},"purchased":{"balance_usd":"3.25"}}`
	balanceLegacy   = `{"included":{"session":{"remaining_percent":75,"resets_at":"2026-11-14T12:00:00Z"},"weekly":{"remaining_percent":140,"resets_at":"2026-11-17T00:00:00Z"}},"purchased":{"balance_usd":0}}`
	usageMonth      = `{"from":"2026-10-15T00:00:00Z","until":"2026-11-14T08:00:00Z","totals":{"request_count":120,"input_tokens":50000,"cached_input_tokens":10000,"output_tokens":8000,"usage_usd":4.2,"groups":[{"model":"gpt-oss:120b","request_count":100,"usage_usd":3.5},{"model":"qwen3-vl:235b","request_count":15,"usage_usd":0.5}],"remainder":{"request_count":5,"usage_usd":0.2}},"buckets":[{"from":"2026-10-20T00:00:00Z","request_count":60,"input_tokens":30000,"output_tokens":5000,"usage_usd":2.0},{"from":"2026-11-08T00:00:00Z","request_count":40,"input_tokens":15000,"cached_input_tokens":8000,"output_tokens":2000,"usage_usd":1.5},{"from":"2026-11-14T00:00:00Z","request_count":20,"input_tokens":5000,"cached_input_tokens":2000,"output_tokens":1000,"usage_usd":0.7}]}`
	usageDay        = `{"from":"2026-11-13T08:00:00Z","until":"2026-11-14T08:00:00Z","totals":{"request_count":20,"input_tokens":5000,"cached_input_tokens":2000,"output_tokens":1000,"usage_usd":0.7}}`
)

func accountOK(balance string) []snapshotReply {
	return []snapshotReply{
		replyJSON(http.MethodGet, "/api/balance", 200, balance),
		replyJSON(http.MethodGet, "/api/usage", 200, usageMonth),
		replyJSON(http.MethodGet, "/api/usage", 200, usageDay),
	}
}

func concat(groups ...[]snapshotReply) []snapshotReply {
	var out []snapshotReply
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

// ---------------------------------------------------------------------------
// RPC steps

func step(method string, request any) snapshotStep {
	if request == nil {
		request = struct{}{}
	}
	return snapshotStep{RPC: snapshotRPC{Method: method, Request: snapJSON(request)}}
}

// holdRefresh marks a case whose plugin.register / plugin.reconfigure steps
// must not run the catalog refresh they queue.
func holdRefresh(fixture snapshotFixture) snapshotFixture {
	fixture.Setup.DropQueuedRefresh = true
	return fixture
}

// snapExec describes one executor call as CPA makes it.
type snapExec struct {
	cred      snapCred
	format    string // openai | openai-response | claude
	model     string
	payload   string
	stream    bool
	streamID  string
	headers   http.Header
	requestID string
	method    string
}

func formatPath(format string) string {
	switch format {
	case "openai-response":
		return "/v1/responses"
	case "claude":
		return "/v1/messages"
	}
	return "/v1/chat/completions"
}

func execStep(e snapExec) snapshotStep {
	if e.cred.name == "" {
		e.cred = credA
	}
	if e.format == "" {
		e.format = "openai"
	}
	if e.payload == "" {
		e.payload = chatPayload
	}
	headers := http.Header{
		"Authorization": {"Bearer sk-client-fake"},
		"Content-Type":  {"application/json"},
		"User-Agent":    {"snapshot-client/1.0"},
	}
	for key, values := range e.headers {
		headers[http.CanonicalHeaderKey(key)] = values
	}
	if e.requestID != "" {
		headers.Set("X-Ollama-Cpa-Request-Id", e.requestID)
	}
	metadata := map[string]any{
		"request_path":        formatPath(e.format),
		"selected_auth_id":    e.cred.name,
		"selected_auth_index": e.cred.index,
	}
	if e.model != "" {
		// The name the client asked for: CPA strips the auth prefix
		// before calling the executor.
		requested := e.model
		if !strings.Contains(requested, "/") {
			requested = "ollama/" + requested
		}
		metadata["requested_model"] = requested
	}
	method := e.method
	if method == "" {
		method = pluginabi.MethodExecutorExecute
		if e.stream {
			method = pluginabi.MethodExecutorExecuteStream
		}
	}
	return step(method, snapshotExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			AuthID:          e.cred.name,
			AuthProvider:    "ollama",
			Model:           e.model,
			Format:          e.format,
			Stream:          e.stream,
			Headers:         headers,
			OriginalRequest: []byte(e.payload),
			SourceFormat:    e.format,
			Payload:         []byte(e.payload),
			Metadata:        metadata,
			StorageJSON:     e.cred.raw(),
			AuthMetadata:    e.cred.metadata(),
		},
		StreamID:       e.streamID,
		HostCallbackID: "cb-snapshot",
	})
}

func mgmtStep(method, path string, query url.Values, body string) snapshotStep {
	req := pluginapi.ManagementRequest{Method: method, Path: path, Query: query, Headers: http.Header{"Accept": {"application/json"}}}
	if body != "" {
		req.Body = []byte(body)
		req.Headers.Set("Content-Type", "application/json")
	}
	return step(pluginabi.MethodManagementHandle, snapshotManagementRequest{ManagementRequest: req, HostCallbackID: "cb-snapshot"})
}

func modelsForAuthStep(c snapCred, host pluginapi.HostConfigSummary) snapshotStep {
	return step(pluginabi.MethodModelForAuth, snapshotAuthModelRequest{AuthModelRequest: pluginapi.AuthModelRequest{
		AuthID:       c.name,
		AuthProvider: "ollama",
		StorageJSON:  c.raw(),
		Metadata:     c.metadata(),
		Host:         host,
	}, HostCallbackID: "cb-snapshot"})
}

func quotaStep(c snapCred, withStorage bool) snapshotStep {
	req := pluginapi.QuotaFetchRequest{AuthIndex: c.index, AuthID: c.name, Provider: "ollama"}
	if withStorage {
		req.StorageJSON = c.raw()
		req.Metadata = c.metadata()
	}
	return step(pluginabi.MethodQuotaFetch, snapshotQuotaFetchRequest{QuotaFetchRequest: req, HostCallbackID: "cb-snapshot"})
}

func interceptStep(authID, traceID string) snapshotStep {
	return step(pluginabi.MethodRequestInterceptAfter, snapshotInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{
		RequestID:      "req-uuid-1",
		TraceID:        traceID,
		SourceFormat:   "openai",
		ToFormat:       "openai",
		Model:          snapModel,
		RequestedModel: "ollama/" + snapModel,
		Headers:        http.Header{"User-Agent": {"snapshot-client/1.0"}, "X-Ollama-Cpa-Request-Id": {"client-sent"}},
		Metadata:       map[string]any{"selected_auth_id": authID, "selected_auth_index": "idx-1"},
	}})
}

func registerStep(method string, config json.RawMessage, schema uint32) snapshotStep {
	return step(method, cpaLifecycleRequest{ConfigYAML: config, SchemaVersion: schema, MultiAuthProviders: true})
}

// ---------------------------------------------------------------------------
// Cases

// snapshotCases returns the case table with the byte fields of every step
// request decoded, as fixtures show them.
func snapshotCases(t *testing.T) []snapshotFixture {
	cases := snapshotCaseTable()
	for i := range cases {
		for j := range cases[i].Steps {
			cases[i].Steps[j].RPC.Request = snapshotCaseJSON(t, cases[i].Steps[j].RPC.Request)
		}
	}
	return cases
}

func snapshotCaseTable() []snapshotFixture {
	var cases []snapshotFixture
	// Every executor case ends with the request log, which records what the
	// plugin read from the exchange (usage, finish reason, frames, errors).
	for _, fixture := range append(execCases(), errorCases()...) {
		if fixture.Case != "count_tokens" {
			fixture.Steps = append(fixture.Steps, mgmtStep(http.MethodGet, mgmtPrefix+"/logs", nil, ""))
		}
		cases = append(cases, fixture)
	}
	cases = append(cases, catalogCases()...)
	cases = append(cases, credentialCases()...)
	cases = append(cases, quotaCases()...)
	cases = append(cases, managementCases()...)
	cases = append(cases, lifecycleCases()...)
	return cases
}

func execCases() []snapshotFixture {
	return []snapshotFixture{
		snapFixture("exec_chat_nonstream", "Chat Completions, buffered: model rewrite, header allowlist (client Authorization and Anthropic headers dropped), usage", nil,
			creds(credA),
			script(replyJSON(http.MethodPost, "/v1/chat/completions", 200, chatResponse, "X-Request-Id", "up-req-1", "X-Internal", "hidden")),
			execStep(snapExec{model: snapModel, requestID: "trace-chat-1", headers: http.Header{"Anthropic-Version": {"2023-06-01"}, "Anthropic-Beta": {"x"}}})),
		snapFixture("exec_chat_stream", "Chat Completions stream through the host stream bridge: stream_options.include_usage added, keepalive comment dropped, [DONE] kept", nil,
			creds(credA),
			script(replySSE("/v1/chat/completions", chatStreamChunks()...)),
			execStep(snapExec{model: snapModel, payload: `{"model":"gpt-oss:120b","stream":true,"messages":[{"role":"user","content":"Hi"}]}`, stream: true, streamID: "stream-chat-1", requestID: "trace-chat-2"})),
		snapFixture("exec_chat_stream_buffered", "Chat Completions stream without a stream_id: the whole stream comes back as chunks in the RPC response", nil,
			creds(credA),
			script(replySSE("/v1/chat/completions", chatStreamChunks()...)),
			execStep(snapExec{model: snapModel, payload: `{"model":"gpt-oss:120b","stream":true,"messages":[{"role":"user","content":"Hi"}]}`, stream: true})),
		snapFixture("exec_chat_model_suffix", "Prefixed model with a thinking suffix is mapped to the bare upstream ID; a client stream_options is left alone", nil,
			creds(credA),
			script(replySSE("/v1/chat/completions", chatStreamChunks()...)),
			execStep(snapExec{model: "ollama/gpt-oss:120b(high)", payload: `{"model":"ollama/gpt-oss:120b(high)","stream":true,"stream_options":{"include_usage":false},"messages":[]}`, stream: true, streamID: "stream-chat-2"})),
		snapFixture("exec_responses_nonstream", "Responses, buffered, with cached and reasoning token usage", nil,
			creds(credA),
			script(replyJSON(http.MethodPost, "/v1/responses", 200, respResponse, "Request-Id", "up-req-2")),
			execStep(snapExec{format: "openai-response", model: snapModel, payload: respPayload})),
		snapFixture("exec_responses_stream", "Responses stream: prelude events held until output, completed event ends it", nil,
			creds(credA),
			script(replySSE("/v1/responses", responsesStreamChunks()...)),
			execStep(snapExec{format: "openai-response", model: snapModel, payload: `{"model":"gpt-oss:120b","input":"Hi","stream":true}`, stream: true, streamID: "stream-resp-1"})),
		snapFixture("exec_messages_nonstream", "Messages, buffered: default Anthropic-Version added, Anthropic-Beta forwarded, x-api-key not forwarded, thinking budget logged", nil,
			creds(credA),
			script(replyJSON(http.MethodPost, "/v1/messages", 200, msgResponse)),
			execStep(snapExec{format: "claude", model: snapModel, payload: msgPayload, headers: http.Header{"Anthropic-Beta": {"interleaved-thinking-2025-05-14"}, "X-Api-Key": {"sk-client-fake"}}})),
		snapFixture("exec_messages_stream", "Messages stream with the client's Anthropic-Version; usage from message_start and message_delta", nil,
			creds(credA),
			script(replySSE("/v1/messages", messagesStreamChunks()...)),
			execStep(snapExec{format: "claude", model: snapModel, payload: `{"model":"gpt-oss:120b","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Hi"}],"output_config":{"effort":"medium"}}`, stream: true, streamID: "stream-msg-1", headers: http.Header{"Anthropic-Version": {"2023-06-01"}}})),
		snapFixture("exec_credential_overrides", "Auth file base_url (with /api suffix) overrides the config base-url, its prefix is stripped and proxy_url direct is the egress", snapConfig(map[string]any{"base-url": "http://config-base.invalid"}),
			creds(cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "base_url": "{{UPSTREAM}}/api/", "prefix": "oc", "proxy_url": "direct"})),
			script(replyJSON(http.MethodPost, "/v1/chat/completions", 200, chatResponse)),
			execStep(snapExec{cred: cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "base_url": "{{UPSTREAM}}/api/", "prefix": "oc", "proxy_url": "direct"}), model: "oc/gpt-oss:120b", payload: `{"model":"oc/gpt-oss:120b","messages":[]}`})),
		snapFixture("count_tokens", "executor.count_tokens is rejected without calling Ollama", nil,
			creds(credA), nil,
			execStep(snapExec{format: "claude", model: snapModel, payload: msgPayload, method: pluginabi.MethodExecutorCountTokens})),
	}
}

func errorCases() []snapshotFixture {
	chatError := func(name, description string, r snapshotReply) snapshotFixture {
		return snapFixture(name, description, nil, creds(credA), script(r), execStep(snapExec{model: snapModel, requestID: "trace-" + name}))
	}
	noKey := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "label": "no key"})
	badProxy := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "proxy_url": "ftp://proxy.invalid:21"})
	return []snapshotFixture{
		chatError("error_400", "Upstream 400 with an OpenAI-style error body", replyJSON(http.MethodPost, "/v1/chat/completions", 400, `{"error":{"message":"messages: field required","type":"invalid_request_error","code":"invalid_request"}}`)),
		chatError("error_401", "Upstream 401 keeps its status so CPA cools the key down", replyJSON(http.MethodPost, "/v1/chat/completions", 401, `{"error":{"message":"invalid api key","type":"authentication_error"}}`)),
		snapFixture("error_403", "Upstream 403 with an Anthropic-style error body on Messages", nil, creds(credA),
			script(replyJSON(http.MethodPost, "/v1/messages", 403, `{"type":"error","error":{"type":"permission_error","message":"account suspended"}}`)),
			execStep(snapExec{format: "claude", model: snapModel, payload: msgPayload})),
		chatError("error_404_model", "Upstream 404 (model not found) becomes a 400 that does not cool the key", replyJSON(http.MethodPost, "/v1/chat/completions", 404, `{"error":"model \"nope:1b\" not found"}`)),
		chatError("error_429_retry_after", "Upstream 429 keeps Retry-After as retry_after_seconds", replyJSON(http.MethodPost, "/v1/chat/completions", 429, `{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`, "Retry-After", "30")),
		chatError("error_500", "Upstream 500 is retryable", replyJSON(http.MethodPost, "/v1/chat/completions", 500, `{"error":{"message":"internal error"}}`)),
		chatError("error_529_overloaded", "Upstream 529 becomes a retryable 503", replyJSON(http.MethodPost, "/v1/chat/completions", 529, `{"error":{"message":"overloaded","type":"overloaded_error"}}`)),
		chatError("error_plain_text", "Upstream 502 with a plain-text body", reply(http.MethodPost, "/v1/chat/completions", 502, "text/plain; charset=utf-8", "upstream connect error or disconnect/reset before headers\n")),
		chatError("error_string_body", `Upstream 400 with the native {"error":"msg"} body`, replyJSON(http.MethodPost, "/v1/chat/completions", 400, `{"error":"messages must not be empty"}`)),
		snapFixture("error_stream_http_429", "Stream request answered with HTTP 429 fails the RPC itself so CPA can retry another key", nil, creds(credA),
			script(replyJSON(http.MethodPost, "/v1/chat/completions", 429, `{"error":{"message":"you have reached your hourly limit","type":"rate_limit_error"}}`, "Retry-After", "12")),
			execStep(snapExec{model: snapModel, stream: true, streamID: "stream-err-1"})),
		snapFixture("error_stream_prelude", "Error event inside a 200 stream before any output: nothing is emitted, the host stream is closed with the mapped error", nil, creds(credA),
			script(replySSE("/v1/chat/completions",
				chatChunk(`"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`),
				sse("", `{"error":{"message":"too many requests","type":"rate_limit_error"}}`))),
			execStep(snapExec{model: snapModel, stream: true, streamID: "stream-err-2"})),
		snapFixture("error_stream_prelude_buffered", "Messages error event before output without a stream_id fails the RPC (overloaded maps to 503)", nil, creds(credA),
			script(replySSE("/v1/messages",
				sse("message_start", `{"type":"message_start","message":{"id":"msg_x","type":"message","role":"assistant","model":"gpt-oss:120b","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`),
				sse("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))),
			execStep(snapExec{format: "claude", model: snapModel, payload: msgPayload, stream: true})),
		snapFixture("error_stream_response_failed", "Responses response.failed before output closes the stream with the failure's error", nil, creds(credA),
			script(replySSE("/v1/responses",
				sse("response.created", `{"type":"response.created","sequence_number":0,"response":{"id":"resp_f","status":"in_progress","output":[]}}`),
				sse("response.failed", `{"type":"response.failed","sequence_number":1,"response":{"id":"resp_f","status":"failed","error":{"code":"server_error","message":"model runner crashed"}}}`))),
			execStep(snapExec{format: "openai-response", model: snapModel, payload: respPayload, stream: true, streamID: "stream-err-3"})),
		snapFixture("error_stream_midway", "Error event after output started is passed to the client as is and the stream counts as complete", nil, creds(credA),
			script(replySSE("/v1/chat/completions",
				chatChunk(`"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`),
				sse("", `{"error":{"message":"model crashed","type":"server_error"}}`))),
			execStep(snapExec{model: snapModel, stream: true, streamID: "stream-err-4"})),
		snapFixture("error_stream_truncated", "Responses stream that stops after output without its terminal event is closed as stream_truncated", nil, creds(credA),
			script(replySSE("/v1/responses", responsesStreamChunks()[:3]...)),
			execStep(snapExec{format: "openai-response", model: snapModel, payload: respPayload, stream: true, streamID: "stream-err-5"})),
		snapFixture("error_no_api_key", "Credential without api_key fails before any upstream call and is logged", nil, creds(noKey), nil,
			execStep(snapExec{cred: noKey, model: snapModel})),
		snapFixture("error_no_model", "Request without a model is rejected before any upstream call", nil, creds(credA), nil,
			execStep(snapExec{payload: `{"messages":[]}`})),
		snapFixture("error_invalid_payload", "Payload that is not JSON is rejected before any upstream call", nil, creds(credA), nil,
			execStep(snapExec{model: snapModel, payload: `not json`})),
		snapFixture("error_invalid_proxy", "Credential with an unsupported proxy scheme fails with invalid_proxy", nil, creds(badProxy), nil,
			execStep(snapExec{cred: badProxy, model: snapModel})),
		snapFixture("error_unsupported_format", "Executor format the plugin does not serve", nil, creds(credA), nil,
			execStep(snapExec{format: "gemini", model: snapModel}),
			execStep(snapExec{format: "gemini", model: snapModel, stream: true, streamID: "stream-err-6"})),
	}
}

func catalogCases() []snapshotFixture {
	selected := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "models": []string{"qwen3-vl:235b", "retired:7b"}, "prefix": "oc"})
	stale := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "label": "Main", "model_revision": "stale"})
	staleDisabled := cred("k2.json", "idx-2", map[string]any{"type": "ollama", "api_key": snapKeyB, "disabled": true, "model_revision": "stale"})
	other := cred("k3.json", "idx-3", map[string]any{"type": "codex", "access_token": "fake-token"})
	return []snapshotFixture{
		snapFixture("catalog_model_static", "model.static registers no static models", nil, nil, nil,
			step(pluginabi.MethodModelStatic, pluginapi.StaticModelRequest{Host: snapshotHostSummary, MultiProviderModels: true})),
		snapFixture("catalog_models_for_auth_all", "model.for_auth with an empty catalog loads /api/tags and /api/show, registers all chat models; later calls use the cache", nil,
			creds(credA), catalogThree(),
			modelsForAuthStep(credA, snapshotHostSummary),
			modelsForAuthStep(credA, snapshotHostSummary),
			mgmtStep(http.MethodGet, mgmtPrefix+"/models", nil, "")),
		snapFixture("catalog_models_for_auth_selected", "Selected models: catalog models with metadata, unknown ones with minimal metadata; status lists the missing one", nil,
			creds(selected), catalogThree(),
			modelsForAuthStep(selected, snapshotHostSummary),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, "")),
		snapFixture("catalog_show_thinking_levels", "/api/show fills capabilities, context length, family and named thinking levels", nil,
			creds(credA), catalogOne(showLevels),
			mgmtStep(http.MethodGet, mgmtPrefix+"/models", nil, ""),
			modelsForAuthStep(credA, snapshotHostSummary)),
		snapFixture("catalog_show_thinking_toggle", "/api/show with an on/off thinking toggle and a context length found by suffix", nil,
			creds(credA), catalogOne(showToggle),
			mgmtStep(http.MethodGet, mgmtPrefix+"/models", nil, ""),
			modelsForAuthStep(credA, snapshotHostSummary)),
		snapFixture("catalog_show_error", "A failed /api/show is counted in detail_errors; a forced refresh loads the details again", nil,
			creds(credA),
			script(
				replyJSON(http.MethodGet, "/api/tags", 200, tagsOne),
				reply(http.MethodPost, "/api/show", 500, "text/plain", "boom"),
				replyJSON(http.MethodGet, "/api/tags", 200, tagsOne),
				replyJSON(http.MethodPost, "/api/show", 200, showNoDetail)),
			mgmtStep(http.MethodGet, mgmtPrefix+"/models", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/models/refresh", nil, "")),
		snapFixture("catalog_tags_error", "/api/tags failing: models view 502, model.for_auth registers nothing, status shows the error", nil,
			creds(credA),
			script(
				replyJSON(http.MethodGet, "/api/tags", 503, `{"error":"service unavailable"}`),
				replyJSON(http.MethodGet, "/api/tags", 200, `{"models":[]}`)),
			mgmtStep(http.MethodGet, mgmtPrefix+"/models", nil, ""),
			modelsForAuthStep(credA, snapshotHostSummary),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, "")),
		snapFixture("catalog_refresh_resync", "Forced refresh re-saves auth files with an older model_revision (disabled ones too, non-Ollama ones never); a second refresh saves nothing", nil,
			creds(stale, staleDisabled, other), concat(catalogThree(), catalogThree()),
			mgmtStep(http.MethodPost, mgmtPrefix+"/models/refresh", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/models/refresh", nil, ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, "")),
		holdRefresh(snapFixture("catalog_reconfigure_resync", "Changing model-prefix by plugin.reconfigure marks registered credentials stale, so the next refresh re-saves them", nil,
			creds(credA), concat(catalogOne(showLevels), catalogOne(showLevels), catalogOne(showLevels)),
			mgmtStep(http.MethodPost, mgmtPrefix+"/models/refresh", nil, ""),
			registerStep(pluginabi.MethodPluginReconfigure, snapConfig(map[string]any{"model-prefix": "ol2/"}), pluginabi.SchemaVersion),
			mgmtStep(http.MethodPost, mgmtPrefix+"/models/refresh", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/models/refresh", nil, ""))),
	}
}

func credentialCases() []snapshotFixture {
	existing := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "label": "Old", "prefix": "oc", "models": []string{"gpt-oss:120b", "old:1b"}})
	full := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "label": "Full", "note": "primary", "priority": 3, "base_url": "https://ollama.example/v1", "prefix": "", "models": []string{"b-model", "a-model", "a-model"}, "proxy_url": "http://user:pass@10.0.0.9:3128"})
	disabled := cred("k2.json", "idx-2", map[string]any{"type": "ollama", "api_key": snapKeyB, "disabled": true})
	other := cred("k3.json", "idx-3", map[string]any{"type": "codex", "access_token": "fake-token"})
	keyless := cred("k4.json", "idx-4", map[string]any{"type": "ollama"})
	addBody := `{"api_key":"` + snapKeyB + `","label":" Main key ","note":"team","models":["gpt-oss:120b","missing:1b"],"prefix":"/oc/"}`
	parse := func(name string, raw string) snapshotStep {
		return step(pluginabi.MethodAuthParse, pluginapi.AuthParseRequest{Provider: "ollama", Path: snapshotAuthDir + "/" + name, FileName: name, RawJSON: []byte(raw), Host: snapshotHostSummary})
	}
	return []snapshotFixture{
		snapFixture("credential_add", "Adding a key: duplicate check, /api/balance key check, catalog load, auth file save (re-parsed by CPA), credential view; adding it again is a 409", nil,
			creds(credA), concat(script(replyJSON(http.MethodGet, "/api/balance", 200, balanceIncluded)), catalogThree()),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, addBody),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, addBody)),
		snapFixture("credential_add_skip_verify", "skip_verify saves without the key check; proxy_url direct is used for the catalog load", nil,
			nil, catalogOne(showLevels),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","skip_verify":true,"proxy_url":"direct","priority":2}`)),
		snapFixture("credential_add_rejected", "Key check failures: 401 and 403 are 400 for the page, 429 and 500 are 502", nil,
			nil,
			script(
				replyJSON(http.MethodGet, "/api/balance", 401, `{"error":"unauthorized"}`),
				replyJSON(http.MethodGet, "/api/balance", 403, `{"error":"account suspended"}`),
				replyJSON(http.MethodGet, "/api/balance", 429, `{"error":"too many requests"}`, "Retry-After", "60"),
				replyJSON(http.MethodGet, "/api/balance", 500, `{"error":"internal"}`)),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`"}`)),
		snapFixture("credential_add_invalid", "Input validation of the add route", nil,
			nil, nil,
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":""}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"has space"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","proxy_id":"nope"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","proxy_url":"ftp://proxy.invalid"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","base_url":"ftp://ollama.invalid"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","prefix":"a b"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","models":"all"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","models":["a b"]}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/credentials", nil, `{"api_key":"`+snapKeyA+`","priority":"high"}`)),
		snapFixture("credential_update", "Editing a credential (by auth_index and by name), the immutable key, unknown credentials, and DELETE (removal is CPA's own auth-file API)", nil,
			creds(existing), catalogOne(showLevels),
			mgmtStep(http.MethodGet, mgmtPrefix+"/models", nil, ""),
			mgmtStep(http.MethodPatch, mgmtPrefix+"/credentials", nil, `{"auth_index":"idx-1","label":"New","note":" hello ","models":null,"prefix":null,"priority":5,"proxy_url":"socks5://user:secret@10.0.0.1:1080"}`),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, ""),
			mgmtStep(http.MethodPatch, mgmtPrefix+"/credentials", nil, `{"name":"k1.json","label":"","models":["gpt-oss:120b","missing:1b"],"priority":0,"proxy_url":""}`),
			mgmtStep(http.MethodPatch, mgmtPrefix+"/credentials", nil, `{"auth_index":"idx-1","api_key":"`+snapKeyB+`"}`),
			mgmtStep(http.MethodPatch, mgmtPrefix+"/credentials", nil, `{"auth_index":"idx-9","label":"x"}`),
			mgmtStep(http.MethodPatch, mgmtPrefix+"/credentials", nil, `{"auth_index":"idx-1","priority":1.5}`),
			mgmtStep(http.MethodDelete, mgmtPrefix+"/credentials", snapshotQuery("auth_index", "idx-1"), "")),
		snapFixture("credential_status_mixed", "Status view over a full auth file (redacted proxy, normalised models), a disabled one, a keyless one; non-Ollama files are skipped", nil,
			creds(full, disabled, other, keyless), nil,
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, "")),
		snapFixture("credential_auth_parse", "auth.parse: labelled file, label from the file name, non-Ollama file, keyless file, invalid JSON", nil,
			nil, nil,
			parse("ollama-main.json", `{"type":"ollama","api_key":"`+snapKeyA+`","label":"Main","prefix":"oc/","proxy_url":"direct","priority":2}`),
			parse("ollama-plain.json", `{"type":"Ollama","api_key":"`+snapKeyB+`","disabled":true}`),
			parse("codex.json", `{"type":"codex","access_token":"fake-token"}`),
			parse("keyless.json", `{"type":"ollama"}`),
			parse("broken.json", `{"type":`)),
		snapFixture("credential_auth_refresh", "auth.refresh merges CPA metadata over the stored file and schedules the next refresh a year out", nil,
			nil, nil,
			step(pluginabi.MethodAuthRefresh, snapshotAuthRefreshRequest{AuthRefreshRequest: pluginapi.AuthRefreshRequest{
				AuthID:       "k1.json",
				AuthProvider: "ollama",
				StorageJSON:  credA.raw(),
				Metadata:     map[string]any{"label": "From metadata", "prefix": "meta"},
				Host:         snapshotHostSummary,
			}}),
			step(pluginabi.MethodAuthRefresh, snapshotAuthRefreshRequest{AuthRefreshRequest: pluginapi.AuthRefreshRequest{
				AuthID:      "k4.json",
				StorageJSON: []byte(`{"type":"ollama"}`),
			}})),
	}
}

func quotaCases() []snapshotFixture {
	return []snapshotFixture{
		snapFixture("quota_fetch", "quota.fetch: balance, 30d (with models) and 24h usage in one egress turn; a complete answer is cached for the next fetch and the page view", nil,
			creds(credA), accountOK(balanceIncluded),
			quotaStep(credA, true),
			quotaStep(credA, true),
			mgmtStep(http.MethodGet, mgmtPrefix+"/credentials/quota", snapshotQuery("auth_index", "idx-1"), "")),
		snapFixture("quota_fetch_legacy_plan", "Legacy plan balance: session and weekly limits (clamped to 100%) as quota buckets", nil,
			creds(credA), accountOK(balanceLegacy),
			quotaStep(credA, true)),
		snapFixture("quota_fetch_401", "Balance 401 skips the usage calls and fails the fetch", nil,
			creds(credA), script(replyJSON(http.MethodGet, "/api/balance", 401, `{"error":"unauthorized"}`)),
			quotaStep(credA, true)),
		snapFixture("quota_fetch_429", "Balance 429 skips the usage calls and fails with Retry-After", nil,
			creds(credA), script(replyJSON(http.MethodGet, "/api/balance", 429, `{"error":"rate limited"}`, "Retry-After", "45")),
			quotaStep(credA, true)),
		snapFixture("quota_fetch_partial", "Usage failing next to a good balance still answers (not cached); balance failing with 500 still reads usage", nil,
			creds(credA),
			script(
				replyJSON(http.MethodGet, "/api/balance", 200, balanceIncluded),
				replyJSON(http.MethodGet, "/api/usage", 500, `{"error":"usage unavailable"}`),
				replyJSON(http.MethodGet, "/api/usage", 200, usageDay),
				replyJSON(http.MethodGet, "/api/balance", 500, `{"error":"balance unavailable"}`),
				replyJSON(http.MethodGet, "/api/usage", 200, usageMonth),
				replyJSON(http.MethodGet, "/api/usage", 502, `bad gateway`)),
			quotaStep(credA, true),
			quotaStep(credA, true)),
		snapFixture("quota_fetch_all_fail", "Balance and both usage calls failing fails the fetch with the balance error", nil,
			creds(credA),
			script(
				replyJSON(http.MethodGet, "/api/balance", 500, `{"error":"balance down"}`),
				replyJSON(http.MethodGet, "/api/usage", 500, `{"error":"usage down"}`),
				replyJSON(http.MethodGet, "/api/usage", 503, `{"error":"usage down"}`)),
			quotaStep(credA, true)),
		snapFixture("quota_fetch_from_auth_file", "quota.fetch without storage reads the auth file by auth_index; an unknown index has no key", nil,
			creds(credA), accountOK(balanceIncluded),
			quotaStep(credA, false),
			quotaStep(cred("k9.json", "idx-9", nil), false)),
		snapFixture("quota_management_view", "Quota page view: account fingerprint, balance, 24h / 7d / 30d usage and per-model breakdown; unknown auth_index is 404", nil,
			creds(credA), accountOK(balanceIncluded),
			mgmtStep(http.MethodGet, mgmtPrefix+"/credentials/quota", snapshotQuery("auth_index", "idx-1"), ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/credentials/quota", snapshotQuery("auth_index", "idx-9"), "")),
		snapFixture("quota_management_errors", "Quota page errors: an upstream 4xx is a 400 for the page, all calls failing with 5xx is a 502", nil,
			creds(credA, credB),
			script(
				replyJSON(http.MethodGet, "/api/balance", 401, `{"error":"unauthorized"}`),
				replyJSON(http.MethodGet, "/api/balance", 500, `{"error":"down"}`),
				replyJSON(http.MethodGet, "/api/usage", 500, `{"error":"down"}`),
				replyJSON(http.MethodGet, "/api/usage", 500, `{"error":"down"}`)),
			mgmtStep(http.MethodGet, mgmtPrefix+"/credentials/quota", snapshotQuery("auth_index", "idx-1"), ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/credentials/quota", snapshotQuery("auth_index", "idx-2"), "")),
	}
}

func managementCases() []snapshotFixture {
	direct := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "proxy_url": "direct"})
	proxied := cred("k1.json", "idx-1", map[string]any{"type": "ollama", "api_key": snapKeyA, "proxy_url": "socks5://10.9.9.9:1080"})
	return []snapshotFixture{
		snapFixture("management_status_empty", "Status with no credentials: config view and empty catalog", nil, nil, nil,
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, "")),
		snapFixture("management_status_host_proxy", "CPA's global proxy and force-model-prefix arrive with model.for_auth and show (redacted) in status and the proxy list", nil,
			creds(direct), catalogOne(showLevels),
			modelsForAuthStep(direct, pluginapi.HostConfigSummary{AuthDir: snapshotAuthDir, ProxyURL: "http://admin:hunter2@proxy.example:3128", ForceModelPrefix: true}),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/proxies", nil, "")),
		snapFixture("management_logs", "Request log after a success, an upstream error, a stream and a rejected request: listing, filters, paging, request_id, bad range, clear", nil,
			creds(credA),
			script(
				replyJSON(http.MethodPost, "/v1/chat/completions", 200, chatResponse, "X-Request-Id", "up-log-1"),
				replyJSON(http.MethodPost, "/v1/chat/completions", 429, `{"error":{"message":"slow down","type":"rate_limit_error"}}`, "Retry-After", "7"),
				replySSE("/v1/messages", messagesStreamChunks()...)),
			execStep(snapExec{model: snapModel, requestID: "trace-log-1"}),
			execStep(snapExec{model: snapModel, requestID: "trace-log-2"}),
			execStep(snapExec{format: "claude", model: snapModel, payload: msgPayload, stream: true, streamID: "stream-log-1", requestID: "trace-log-3"}),
			execStep(snapExec{payload: `{"messages":[]}`, requestID: "trace-log-4"}),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", nil, ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", snapshotQuery("status", "error"), ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", snapshotQuery("search", "messages"), ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", snapshotQuery("limit", "1", "offset", "1"), ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", snapshotQuery("request_id", "trace-log-1"), ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", snapshotQuery("from", "yesterday"), ""),
			mgmtStep(http.MethodDelete, mgmtPrefix+"/logs", nil, ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", nil, "")),
		snapFixture("management_proxies", "Proxy list: add (redacted), duplicate, invalid, import from credentials, direct / inherit egress checks against the fake lookup service, unknown ids", nil,
			creds(proxied),
			script(
				replyJSON(http.MethodGet, "/json", 200, `{"ip":"203.0.113.7","city":"Tokyo","region":"Tokyo","country":"JP"}`),
				replyJSON(http.MethodGet, "/json", 200, `{"ip":"198.51.100.4","country_code":"us","region":"California","city":"San Jose"}`)),
			mgmtStep(http.MethodGet, mgmtPrefix+"/proxies", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies", nil, `{"name":"Tokyo","url":"http://alice:secret@10.1.2.3:8080"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies", nil, `{"name":"Again","url":"http://alice:secret@10.1.2.3:8080"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies", nil, `{"name":"Bad","url":"ftp://10.1.2.3"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies/import", nil, ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/proxies", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies/check", nil, `{"target":"direct"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies/check", nil, `{"target":"inherit"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies/check", nil, `{"target":"mars"}`),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies/check", nil, `{"id":"missing"}`),
			mgmtStep(http.MethodPatch, mgmtPrefix+"/proxies", nil, `{"id":"missing","name":"x"}`),
			mgmtStep(http.MethodDelete, mgmtPrefix+"/proxies", snapshotQuery("id", "missing"), ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/proxies/order", nil, `{"ids":["missing"]}`),
			mgmtStep(http.MethodGet, mgmtPrefix+"/proxies", nil, ""),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, "")),
		snapFixture("management_routes", "management.register routes and resources; unknown route, wrong method, unknown flag", nil, nil, nil,
			step(pluginabi.MethodManagementRegister, nil),
			mgmtStep(http.MethodGet, mgmtPrefix+"/nope", nil, ""),
			mgmtStep(http.MethodPost, mgmtPrefix+"/status", nil, ""),
			mgmtStep(http.MethodGet, "/v0/resource/plugins/ollama/flag.svg", snapshotQuery("cc", "zz"), "")),
		snapFixture("intercept_request_id", "After-auth interceptor stamps CPA's trace ID only for Ollama auths it has seen; the executor logs it and never forwards it", nil,
			creds(credA), concat(catalogOne(showLevels), script(replyJSON(http.MethodPost, "/v1/chat/completions", 200, chatResponse))),
			interceptStep("k1.json", "trace-abc"),
			modelsForAuthStep(credA, snapshotHostSummary),
			interceptStep("k1.json", "trace-abc"),
			interceptStep("k1.json", ""),
			interceptStep("other.json", "trace-abc"),
			step(pluginabi.MethodRequestInterceptBefore, snapshotInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{TraceID: "trace-abc"}}),
			execStep(snapExec{model: snapModel, requestID: "trace-abc"}),
			mgmtStep(http.MethodGet, mgmtPrefix+"/logs", snapshotQuery("request_id", "trace-abc"), "")),
	}
}

func lifecycleCases() []snapshotFixture {
	return []snapshotFixture{
		holdRefresh(snapFixture("lifecycle_register", "plugin.register / plugin.reconfigure: schema negotiation (interceptor only from schema 7), metadata, config fields, config warnings", nil, nil, nil,
			registerStep(pluginabi.MethodPluginRegister, snapConfig(nil), pluginabi.SchemaVersion),
			registerStep(pluginabi.MethodPluginReconfigure, snapConfig(map[string]any{"models-refresh-minutes": -5, "request-timeout-seconds": 0, "log-retention": -1}), 5),
			registerStep(pluginabi.MethodPluginReconfigure, snapConfig(map[string]any{"base-url": "ftp://bad"}), 0),
			mgmtStep(http.MethodGet, mgmtPrefix+"/status", nil, ""))),
		snapFixture("lifecycle_identifiers", "Identifier and describe RPCs, unsupported operations, unknown method, shutdown", nil, nil, nil,
			step(pluginabi.MethodAuthIdentifier, nil),
			step(pluginabi.MethodExecutorIdentifier, nil),
			step(pluginabi.MethodQuotaIdentifier, nil),
			step(pluginabi.MethodQuotaDescribe, nil),
			step(pluginabi.MethodPluginQuiesce, nil),
			step(pluginabi.MethodAuthLoginStart, nil),
			step(pluginabi.MethodAuthLoginPoll, nil),
			step(pluginabi.MethodExecutorHTTPRequest, nil),
			step(pluginabi.MethodQuotaReset, nil),
			step("model.register", nil),
			step(pluginabi.MethodPluginShutdown, nil)),
	}
}

// CPA's RPC request shapes (internal/pluginhost/rpc_schema.go in CPA v8).

type snapshotExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type snapshotAuthModelRequest = cpaAuthModelRequest

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
