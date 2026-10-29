// Package opencodefree reports local OpenCode activity for models priced at
// zero by OpenCode's local model catalog. This is not a quota check.
package opencodefree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const name = "opencode_free"

const maxCatalogBytes = 8 << 20
const maxSQLiteOutput = 4 << 20
const maxModels = 256

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type Provider struct {
	dbPath      string
	catalogPath string
	run         commandRunner
	now         func() time.Time
}

func New() *Provider {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	cache, _ := os.UserCacheDir()
	return &Provider{dbPath: filepath.Join(data, "opencode", "opencode.db"), catalogPath: filepath.Join(cache, "opencode", "models.json"), run: runSQLite, now: time.Now}
}

func (*Provider) Name() string        { return name }
func (*Provider) DisplayName() string { return "OpenCode Free" }
func (*Provider) Description() string {
	return "Local OpenCode activity and zero-cost model candidates; availability is not checked"
}
func (*Provider) DashboardURL() string     { return "https://opencode.ai/" }
func (*Provider) SafeForAutoPolling() bool { return true }

func (p *Provider) IsConfigured() bool { return p.SetupStatus().IsReady() }
func (p *Provider) SetupStatus() provider.SetupStatus {
	if !regularFile(p.dbPath) {
		return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "OpenCode local database not found"}
	}
	if !regularFile(p.catalogPath) {
		return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "OpenCode local model catalog not found"}
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return provider.SetupStatus{State: provider.SetupUnavailable, Detail: "sqlite3 command not found"}
	}
	return provider.SetupStatus{State: provider.SetupReady, Detail: "OpenCode local activity and model catalog found"}
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	nowFn := p.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalog, err := readCatalog(p.catalogPath)
	if err != nil {
		return nil, err
	}
	rows, err := p.readMessages(ctx, now)
	if err != nil {
		return nil, err
	}
	models := map[string]*provider.LocalModelUsage{}
	missingCounters := map[string]uint{}
	catalogIDs := make(map[string]struct{}, len(catalog))
	for id, meta := range catalog {
		catalogIDs[id] = struct{}{}
		if !meta.ZeroCost() {
			continue
		}
		label := meta.Name
		if label == "" {
			label = id
		}
		z := int64(0)
		models[id] = &provider.LocalModelUsage{Model: id, Label: label, Day: now.Format("2006-01-02"), AssistantMessages: &z}
	}
	for _, r := range rows {
		m := models[r.Model]
		if m == nil {
			if _, found := catalogIDs[r.Model]; found {
				continue
			}
			if !strings.HasSuffix(r.Model, "-free") {
				continue
			}
			m = &provider.LocalModelUsage{Model: r.Model, Label: r.Model, Day: now.Format("2006-01-02")}
			models[r.Model] = m
		}
		if len(models) > maxModels {
			return nil, fmt.Errorf("OpenCode local model list exceeds %d models", maxModels)
		}
		if r.ErrorType == "provider.free-limit" && r.ErrorStatus == 429 {
			at := time.UnixMilli(r.Created).UTC()
			if at.After(m.LimitHitAt) {
				m.LimitHitAt = at
				m.RetryAt = time.Time{}
				if r.RetryAfterMS != nil && *r.RetryAfterMS >= 0 && *r.RetryAfterMS <= math.MaxInt64/int64(time.Millisecond) {
					m.RetryAt = at.Add(time.Duration(*r.RetryAfterMS) * time.Millisecond)
				}
			}
			continue
		}
		if r.ErrorType != "" {
			continue
		}
		if m.AssistantMessages == nil {
			z := int64(0)
			m.AssistantMessages = &z
		}
		*m.AssistantMessages++
		for i, n := range []*int64{r.Input, r.Output, r.Reasoning, r.CacheRead, r.CacheWrite} {
			if n == nil {
				missingCounters[r.Model] |= 1 << i
			}
		}
		if err := addCounter(&m.InputTokens, r.Input); err != nil {
			return nil, err
		}
		if err := addCounter(&m.OutputTokens, r.Output); err != nil {
			return nil, err
		}
		if err := addCounter(&m.ReasoningTokens, r.Reasoning); err != nil {
			return nil, err
		}
		if err := addCounter(&m.CacheReadTokens, r.CacheRead); err != nil {
			return nil, err
		}
		if err := addCounter(&m.CacheWriteTokens, r.CacheWrite); err != nil {
			return nil, err
		}
	}
	ordered := make([]provider.LocalModelUsage, 0, len(models))
	for _, m := range models {
		for i, dst := range []**int64{&m.InputTokens, &m.OutputTokens, &m.ReasoningTokens, &m.CacheReadTokens, &m.CacheWriteTokens} {
			if missingCounters[m.Model]&(1<<i) != 0 {
				*dst = nil
			}
		}
		ordered = append(ordered, *m)
	}
	// Sort for stable output without exposing any session text.
	sortLocalModels(ordered)
	return &provider.UsageData{Provider: name, FetchedAt: now, LocalModels: ordered}, nil
}

type messageRow struct {
	Created      int64  `json:"created"`
	Model        string `json:"model"`
	Input        *int64 `json:"input"`
	Output       *int64 `json:"output"`
	Reasoning    *int64 `json:"reasoning"`
	CacheRead    *int64 `json:"cache_read"`
	CacheWrite   *int64 `json:"cache_write"`
	ErrorType    string `json:"error_type"`
	ErrorStatus  int64  `json:"error_status"`
	RetryAfterMS *int64 `json:"retry_after_ms"`
}

const sqlQuery = `SELECT time_created AS created,
 json_extract(data,'$.model.id') AS model,
 json_extract(data,'$.tokens.input') AS input,
 json_extract(data,'$.tokens.output') AS output,
 json_extract(data,'$.tokens.reasoning') AS reasoning,
 json_extract(data,'$.tokens.cache.read') AS cache_read,
 json_extract(data,'$.tokens.cache.write') AS cache_write,
 json_extract(data,'$.error.type') AS error_type,
 COALESCE(json_extract(data,'$.error.status'),0) AS error_status,
 json_extract(data,'$.error.retryAfterMs') AS retry_after_ms
 FROM session_message
 WHERE type='assistant' AND time_created >= %d AND time_created < %d
 AND json_extract(data,'$.model.providerID')='opencode'
 ORDER BY time_created;`

func (p *Provider) readMessages(ctx context.Context, now time.Time) ([]messageRow, error) {
	if !regularFile(p.dbPath) {
		return nil, fmt.Errorf("OpenCode local database is missing or not a regular file")
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	end := now.UnixMilli() + 1 // Do not count future-dated local records.
	query := fmt.Sprintf(sqlQuery, start, end)
	if p.run == nil {
		p.run = runSQLite
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := p.run(ctx, "sqlite3", "-readonly", "-json", p.dbPath, query)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode local activity: %w", err)
	}
	if len(b) > maxSQLiteOutput {
		return nil, errors.New("OpenCode local activity output exceeds 4 MiB")
	}
	var rows []messageRow
	if len(b) == 0 {
		return rows, nil
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("decode OpenCode local activity: %w", err)
	}
	for _, r := range rows {
		if r.Created < start || r.Created >= end {
			return nil, errors.New("OpenCode returned activity outside the requested UTC day")
		}
		if r.Model == "" {
			return nil, errors.New("OpenCode activity has no model ID")
		}
		for _, n := range []*int64{r.Input, r.Output, r.Reasoning, r.CacheRead, r.CacheWrite, r.RetryAfterMS} {
			if n != nil && *n < 0 {
				return nil, errors.New("OpenCode activity has a negative counter")
			}
		}
	}
	return rows, nil
}

func runSQLite(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite3 failed: %w", err)
	}
	if stdout.overflow {
		return nil, errors.New("sqlite3 output exceeds 4 MiB")
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	b        []byte
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := maxSQLiteOutput - len(b.b)
	if left <= 0 {
		b.overflow = true
		return n, nil
	}
	if len(p) > left {
		b.b = append(b.b, p[:left]...)
		b.overflow = true
	} else {
		b.b = append(b.b, p...)
	}
	return n, nil
}
func (b *limitedBuffer) Bytes() []byte  { return b.b }
func (b *limitedBuffer) String() string { return string(b.b) }

type catalog struct {
	OpenCode struct {
		Models map[string]modelInfo `json:"models"`
	} `json:"opencode"`
}
type modelInfo struct {
	Name string     `json:"name"`
	ID   string     `json:"id"`
	Cost *modelCost `json:"cost"`
}
type modelCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

func (m modelInfo) ZeroCost() bool {
	return m.Cost != nil && m.Cost.Input != nil && m.Cost.Output != nil && *m.Cost.Input == 0 && *m.Cost.Output == 0 && zeroOrAbsent(m.Cost.CacheRead) && zeroOrAbsent(m.Cost.CacheWrite)
}
func zeroOrAbsent(n *float64) bool { return n == nil || *n == 0 }
func readCatalog(path string) (map[string]modelInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode model catalog: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("OpenCode model catalog is not a regular file")
	}
	if st.Size() > maxCatalogBytes {
		return nil, errors.New("OpenCode model catalog exceeds 8 MiB")
	}
	var c catalog
	d := json.NewDecoder(io.LimitReader(f, maxCatalogBytes+1))
	if err := d.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode OpenCode model catalog: %w", err)
	}
	if err := d.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("OpenCode model catalog has trailing data")
	}
	if c.OpenCode.Models == nil {
		return nil, errors.New("OpenCode model catalog has no models object")
	}
	if len(c.OpenCode.Models) > maxModels {
		return nil, fmt.Errorf("OpenCode model catalog exceeds %d models", maxModels)
	}
	return c.OpenCode.Models, nil
}
func regularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
func addCounter(dst **int64, n *int64) error {
	if n == nil {
		return nil
	}
	if *n < 0 {
		return errors.New("OpenCode activity has a negative token count")
	}
	if *dst != nil && **dst > math.MaxInt64-*n {
		return errors.New("OpenCode token count exceeds supported range")
	}
	if *dst == nil {
		z := int64(0)
		*dst = &z
	}
	**dst += *n
	return nil
}
func sortLocalModels(ms []provider.LocalModelUsage) {
	sort.Slice(ms, func(i, j int) bool { return ms[i].Model < ms[j].Model })
}
