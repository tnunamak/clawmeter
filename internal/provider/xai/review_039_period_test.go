package xai

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
)

func TestReview039GrokPreservesActualBillingPeriod(t *testing.T) {
	for _, days := range []int{28, 31} {
		t.Run(strconv.Itoa(days)+"d", func(t *testing.T) {
			period := time.Duration(days) * 24 * time.Hour
			reset := time.Now().Add(period - 2*time.Hour).Truncate(time.Second)
			msg := append(protoFixed32(1, math.Float32bits(1)), protoMessage(5, protoVarint(1, uint64(reset.Unix())))...)
			msg = append(msg, protoMessage(4, protoVarint(1, uint64(reset.Add(-period).Unix())))...)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(grokGRPCWebResponse(protoMessage(1, msg)))
			}))
			defer server.Close()
			p := New(config.ProviderConfig{})
			p.grokHome = writeGrokAuthDir(t, "fake-token", time.Now().Add(time.Hour))
			p.grokBillingURL, p.client = server.URL, server.Client()
			data, err := p.FetchUsage(context.Background())
			if err != nil || data == nil || len(data.Windows) != 1 {
				t.Fatalf("fetch: data=%+v err=%v", data, err)
			}
			w := data.Windows[0]
			got := forecast.Project(w.Utilization, w.ResetsAt, forecast.WindowLength(w))
			want := forecast.Project(w.Utilization, w.ResetsAt, period)
			if w.Length != period || got.Unknown || got.ProjectedPct < 100 {
				t.Fatalf("known period=%s discarded: window=%+v inferred=%+v actual=%+v", period, w, got, want)
			}
		})
	}
}
