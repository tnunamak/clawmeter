package diagnose

import (
	"context"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// RawOutput is the opt-in `--raw` view: what each source's provider actually
// returned for a live fetch. Unlike Output it carries provider text, so bodies
// pass through provider.RedactRaw first.
type RawOutput struct {
	GeneratedAt time.Time   `json:"generated_at"`
	Sources     []RawSource `json:"sources"`
}

type RawSource struct {
	Provider   string     `json:"provider"`
	SourceID   string     `json:"source_id"`
	HTTPStatus int        `json:"http_status,omitempty"`
	FetchedAt  *time.Time `json:"fetched_at,omitempty"`
	RetryAfter string     `json:"retry_after,omitempty"`
	Body       string     `json:"body,omitempty"`
	Note       string     `json:"note,omitempty"`
}

// Raw fetches each provider once and reports its last raw response.
func Raw(ctx context.Context, providers []provider.Provider) RawOutput {
	out := RawOutput{GeneratedAt: time.Now(), Sources: make([]RawSource, 0, len(providers))}
	for _, p := range providers {
		src := RawSource{Provider: p.Name(), SourceID: provider.SourceID(p)}
		reporter, ok := p.(provider.RawReporter)
		if !ok {
			src.Note = "provider does not expose raw responses"
			out.Sources = append(out.Sources, src)
			continue
		}
		_, _ = p.FetchUsage(ctx)
		raw := reporter.LastRawResponse()
		if raw == nil {
			src.Note = "no request made (rate-limit backoff active or credentials unavailable)"
		} else {
			fetchedAt := raw.FetchedAt
			src.HTTPStatus, src.FetchedAt, src.RetryAfter = raw.Status, &fetchedAt, raw.RetryAfter
			src.Body = provider.RedactRaw(raw.Body)
		}
		out.Sources = append(out.Sources, src)
	}
	return out
}
