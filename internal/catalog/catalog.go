// Package catalog caches Ollama's cloud model list and turns it into CPA
// model registrations.
package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/berry-shake/cpa-ollama-plugin/internal/ollama"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	// maxCatalogBytes bounds the /api/tags response (the real one is ~10 KB).
	maxCatalogBytes = 8 << 20
	// maxShowBytes bounds one /api/show response.
	maxShowBytes = 4 << 20
	// showConcurrency is how many /api/show requests run at once.
	showConcurrency = 4
)

// Model is one cloud model from GET /api/tags, completed with the details of
// POST /api/show. The detail fields are zero when they could not be loaded.
type Model struct {
	// ID is the name direct cloud requests use, such as "gpt-oss:120b".
	ID           string
	ModifiedAt   string
	Size         int64
	Digest       string
	Capabilities []string

	Family        string
	ParameterSize string
	Quantization  string
	ContextLength int64
	// ThinkingLevels are the named reasoning levels the model accepts, such
	// as low / medium / high. ThinkingToggle is set instead for models that
	// only turn thinking on or off. ThinkingDefault is the level (or "true" /
	// "false") used when a request sets none.
	ThinkingLevels  []string
	ThinkingToggle  bool
	ThinkingDefault string
}

// Has reports whether the model lists a capability ("completion", "tools",
// "thinking", "vision", ...).
func (m Model) Has(capability string) bool {
	return slices.Contains(m.Capabilities, capability)
}

// Chat reports whether the model generates text. Models without the
// "completion" capability (embedding or decision models) are left out when a
// key registers all models; a model without capability data counts as chat.
func (m Model) Chat() bool {
	return len(m.Capabilities) == 0 || m.Has("completion")
}

// Created is ModifiedAt as a Unix timestamp, or 0.
func (m Model) Created() int64 {
	when, errParse := time.Parse(time.RFC3339, strings.TrimSpace(m.ModifiedAt))
	if errParse != nil {
		return 0
	}
	return when.Unix()
}

type tagsResponse struct {
	Models []struct {
		Name         string   `json:"name"`
		Model        string   `json:"model"`
		ModifiedAt   string   `json:"modified_at"`
		Size         int64    `json:"size"`
		Digest       string   `json:"digest"`
		Capabilities []string `json:"capabilities"`
	} `json:"models"`
}

type showResponse struct {
	Capabilities []string `json:"capabilities"`
	Details      struct {
		Family            string `json:"family"`
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
	ModelInfo map[string]any `json:"model_info"`
	Thinking  *struct {
		Values  []any `json:"values"`
		Default any   `json:"default"`
	} `json:"thinking"`
}

// details is what /api/show adds to a model, cached per model and digest.
type details struct {
	digest          string
	capabilities    []string
	family          string
	parameterSize   string
	quantization    string
	contextLength   int64
	thinkingLevels  []string
	thinkingToggle  bool
	thinkingDefault string
}

// Snapshot is an immutable view of the cached catalog.
type Snapshot struct {
	Models    []Model
	byID      map[string]Model
	Revision  string
	FetchedAt time.Time
	CheckedAt time.Time
	LastError string
	// DetailErrors counts the models whose /api/show failed in the last
	// fetch; they are listed without (or with older) details.
	DetailErrors int
}

// Empty reports whether no catalog has been loaded.
func (c Snapshot) Empty() bool { return len(c.Models) == 0 }

// Lookup finds a model by its upstream ID.
func (c Snapshot) Lookup(id string) (Model, bool) {
	model, ok := c.byID[id]
	return model, ok
}

// Catalog caches Ollama's model list. /api/tags carries no ETag, so every
// refresh reloads the list; model details are fetched again only for new
// models and models whose digest changed.
type Catalog struct {
	mu       sync.RWMutex
	snapshot Snapshot
	// fetchMu serializes upstream fetches so concurrent model.for_auth calls
	// during startup share one request. It also guards details.
	fetchMu sync.Mutex
	details map[string]details
}

// New returns an empty catalog.
func New() *Catalog {
	return &Catalog{snapshot: Snapshot{byID: map[string]Model{}}, details: map[string]details{}}
}

// Current returns the cached snapshot.
func (c *Catalog) Current() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

// Fetch loads the catalog through client. With force it also reloads the
// details of every model. It returns the new snapshot and whether the
// registered model set changed.
func (c *Catalog) Fetch(ctx context.Context, client *http.Client, baseURL, userAgent string, force bool, now time.Time) (Snapshot, bool, error) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	previous := c.Current()
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+ollama.EndpointTags, nil)
	if errReq != nil {
		return previous, false, errReq
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return c.recordError(now, errDo)
	}
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	_ = resp.Body.Close()
	if errRead != nil {
		return c.recordError(now, errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return c.recordError(now, fmt.Errorf("GET %s returned %d: %s", ollama.EndpointTags, resp.StatusCode, truncate(string(body), 300)))
	}
	var list tagsResponse
	if errUnmarshal := json.Unmarshal(body, &list); errUnmarshal != nil {
		return c.recordError(now, fmt.Errorf("decode %s: %w", ollama.EndpointTags, errUnmarshal))
	}
	models := make([]Model, 0, len(list.Models))
	seen := make(map[string]struct{}, len(list.Models))
	for _, item := range list.Models {
		id := strings.TrimSpace(item.Name)
		if id == "" {
			id = strings.TrimSpace(item.Model)
		}
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, Model{
			ID:           id,
			ModifiedAt:   strings.TrimSpace(item.ModifiedAt),
			Size:         item.Size,
			Digest:       strings.TrimSpace(item.Digest),
			Capabilities: append([]string(nil), item.Capabilities...),
		})
	}
	if len(models) == 0 {
		return c.recordError(now, fmt.Errorf("%s returned no models", ollama.EndpointTags))
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	detailErrors := c.loadDetails(ctx, client, baseURL, userAgent, models, force)

	byID := make(map[string]Model, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	next := Snapshot{
		Models:       models,
		byID:         byID,
		Revision:     catalogRevision(models),
		FetchedAt:    now,
		CheckedAt:    now,
		DetailErrors: detailErrors,
	}
	c.mu.Lock()
	c.snapshot = next
	c.mu.Unlock()
	return next, next.Revision != previous.Revision, nil
}

// loadDetails fills in the /api/show details of models, a few requests at a
// time, and returns how many failed. Cached details are reused while the
// model's digest is unchanged (unless force); a model whose request fails
// keeps the details it had, if any, and is asked again next time.
func (c *Catalog) loadDetails(ctx context.Context, client *http.Client, baseURL, userAgent string, models []Model, force bool) int {
	type result struct {
		index   int
		details details
		err     error
	}
	var pending []int
	for i, model := range models {
		if cached, ok := c.details[model.ID]; ok && !force && cached.digest == model.Digest {
			continue
		}
		pending = append(pending, i)
	}
	results := make(chan result, len(pending))
	slots := make(chan struct{}, showConcurrency)
	var wg sync.WaitGroup
	for _, index := range pending {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				results <- result{index: index, err: ctx.Err()}
				return
			}
			defer func() { <-slots }()
			loaded, errShow := fetchDetails(ctx, client, baseURL, userAgent, models[index])
			results <- result{index: index, details: loaded, err: errShow}
		}(index)
	}
	wg.Wait()
	close(results)
	failed := 0
	for r := range results {
		if r.err != nil {
			failed++
			continue
		}
		c.details[models[r.index].ID] = r.details
	}
	for i := range models {
		if cached, ok := c.details[models[i].ID]; ok {
			cached.apply(&models[i])
		}
	}
	return failed
}

// fetchDetails calls POST /api/show for one model.
func fetchDetails(ctx context.Context, client *http.Client, baseURL, userAgent string, model Model) (details, error) {
	payload, _ := json.Marshal(map[string]string{"model": model.ID})
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+ollama.EndpointShow, bytes.NewReader(payload))
	if errReq != nil {
		return details{}, errReq
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return details{}, errDo
	}
	defer func() { _ = resp.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxShowBytes))
	if errRead != nil {
		return details{}, errRead
	}
	if resp.StatusCode != http.StatusOK {
		return details{}, fmt.Errorf("POST %s %s returned %d", ollama.EndpointShow, model.ID, resp.StatusCode)
	}
	var show showResponse
	if errUnmarshal := json.Unmarshal(body, &show); errUnmarshal != nil {
		return details{}, fmt.Errorf("decode %s %s: %w", ollama.EndpointShow, model.ID, errUnmarshal)
	}
	out := details{
		digest:        model.Digest,
		capabilities:  show.Capabilities,
		family:        strings.TrimSpace(show.Details.Family),
		parameterSize: strings.TrimSpace(show.Details.ParameterSize),
		quantization:  strings.TrimSpace(show.Details.QuantizationLevel),
		contextLength: contextLength(show.ModelInfo),
	}
	if show.Thinking != nil {
		for _, value := range show.Thinking.Values {
			switch typed := value.(type) {
			case string:
				if level := strings.TrimSpace(typed); level != "" {
					out.thinkingLevels = append(out.thinkingLevels, level)
				}
			case bool:
				out.thinkingToggle = true
			}
		}
		if len(out.thinkingLevels) > 0 {
			out.thinkingToggle = false
		}
		switch typed := show.Thinking.Default.(type) {
		case string:
			out.thinkingDefault = strings.TrimSpace(typed)
		case bool:
			if typed {
				out.thinkingDefault = "true"
			} else {
				out.thinkingDefault = "false"
			}
		}
	}
	return out, nil
}

// apply copies the details into a model. Capabilities from /api/tags win;
// /api/show only fills them in when the list had none.
func (d details) apply(model *Model) {
	if len(model.Capabilities) == 0 {
		model.Capabilities = append([]string(nil), d.capabilities...)
	}
	model.Family = d.family
	model.ParameterSize = d.parameterSize
	model.Quantization = d.quantization
	model.ContextLength = d.contextLength
	model.ThinkingLevels = append([]string(nil), d.thinkingLevels...)
	model.ThinkingToggle = d.thinkingToggle
	model.ThinkingDefault = d.thinkingDefault
}

// contextLength reads "<architecture>.context_length" from model_info,
// falling back to any member ending in ".context_length".
func contextLength(info map[string]any) int64 {
	if arch, ok := info["general.architecture"].(string); ok {
		if value, okValue := info[strings.TrimSpace(arch)+".context_length"].(float64); okValue && value > 0 {
			return int64(value)
		}
	}
	for key, raw := range info {
		if value, ok := raw.(float64); ok && value > 0 && strings.HasSuffix(key, ".context_length") {
			return int64(value)
		}
	}
	return 0
}

func (c *Catalog) recordError(now time.Time, err error) (Snapshot, bool, error) {
	c.mu.Lock()
	c.snapshot.CheckedAt = now
	c.snapshot.LastError = err.Error()
	snapshot := c.snapshot
	c.mu.Unlock()
	return snapshot, false, err
}

// catalogRevision hashes the parts of the catalog that end up in CPA's model
// registry, so only changes there trigger re-registration.
func catalogRevision(models []Model) string {
	hash := sha256.New()
	for _, model := range models {
		capabilities := append([]string(nil), model.Capabilities...)
		sort.Strings(capabilities)
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%s\n",
			model.ID, model.ContextLength, model.Created(), strings.Join(capabilities, ","))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// ModelInfos builds the CPA model list for one credential: every chat model
// of the catalog when all is set, otherwise the selected IDs. Selected IDs
// missing from the catalog are still registered (with minimal metadata) so a
// catalog outage or a retired model does not silently drop routes the user
// picked. IDs are the bare upstream IDs; CPA adds the auth's prefix itself.
func (c Snapshot) ModelInfos(all bool, ids []string) []pluginapi.ModelInfo {
	var selected []Model
	if all {
		selected = make([]Model, 0, len(c.Models))
		for _, model := range c.Models {
			if model.Chat() {
				selected = append(selected, model)
			}
		}
	} else {
		selected = make([]Model, 0, len(ids))
		for _, id := range ids {
			if model, ok := c.Lookup(id); ok {
				selected = append(selected, model)
				continue
			}
			selected = append(selected, Model{ID: id})
		}
	}
	out := make([]pluginapi.ModelInfo, 0, len(selected))
	for _, model := range selected {
		out = append(out, toModelInfo(model))
	}
	return out
}

func toModelInfo(model Model) pluginapi.ModelInfo {
	inputs := []string{"text"}
	if model.Has("vision") {
		inputs = append(inputs, "image")
	}
	var params []string
	if model.Has("tools") {
		params = append(params, "tools")
	}
	if model.Has("thinking") {
		params = append(params, "reasoning")
	}
	return pluginapi.ModelInfo{
		ID:                        model.ID,
		Object:                    "model",
		Created:                   model.Created(),
		OwnedBy:                   ollama.ProviderID,
		Type:                      ollama.ProviderID,
		DisplayName:               model.ID,
		Name:                      model.ID,
		Description:               "Ollama Cloud · " + model.ID,
		InputTokenLimit:           model.ContextLength,
		ContextLength:             model.ContextLength,
		SupportedParameters:       params,
		SupportedInputModalities:  inputs,
		SupportedOutputModalities: []string{"text"},
		// UserDefined keeps CPA from stripping reasoning settings for models
		// it has no thinking metadata for; Ollama maps them per model.
		UserDefined: true,
	}
}

// UpstreamID maps the model CPA hands the executor back to Ollama's ID.
// CPA strips the auth prefix before calling the executor; the prefix is
// still removed here in case a model arrives with it.
func (c Snapshot) UpstreamID(model, prefix string) string {
	model = stripThinkingSuffix(strings.TrimSpace(model))
	if prefix != "" && strings.HasPrefix(model, prefix+"/") {
		return strings.TrimPrefix(model, prefix+"/")
	}
	if _, ok := c.Lookup(model); ok {
		return model
	}
	// A prefix we do not know (for example from a changed config) is dropped
	// when what follows it is a catalog model.
	if idx := strings.LastIndex(model, "/"); idx >= 0 && idx < len(model)-1 {
		if _, ok := c.Lookup(model[idx+1:]); ok {
			return model[idx+1:]
		}
	}
	return model
}

// stripThinkingSuffix removes a trailing CPA reasoning suffix such as
// "(high)" or "(8192)". CPA normally strips it before calling the executor.
func stripThinkingSuffix(model string) string {
	if !strings.HasSuffix(model, ")") {
		return model
	}
	open := strings.LastIndex(model, "(")
	if open <= 0 {
		return model
	}
	return strings.TrimSpace(model[:open])
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}
