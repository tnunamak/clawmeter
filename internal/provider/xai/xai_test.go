package xai

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestTransformBalance_ComputesCreditUsage(t *testing.T) {
	p := New(config.ProviderConfig{})
	data := p.transformBalance(&prepaidBalanceResponse{
		Changes: []balanceChange{
			{TopupStatus: "SUCCEEDED", Amount: centsValue{Val: -2500}},
			{TopupStatus: "SUCCEEDED", Amount: centsValue{Val: 500}},
			{TopupStatus: "FAILED", Amount: centsValue{Val: -1000}},
		},
		Total: centsValue{Val: -1000},
	})

	if len(data.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(data.Windows))
	}
	w := data.Windows[0]
	if w.Name != "credits" {
		t.Fatalf("window name = %q, want credits", w.Name)
	}
	if w.Limit != 2500 {
		t.Fatalf("limit = %d, want 2500", w.Limit)
	}
	if w.Used != 1500 {
		t.Fatalf("used = %d, want 1500", w.Used)
	}
	if w.Utilization != 60 {
		t.Fatalf("utilization = %.2f, want 60", w.Utilization)
	}
}

func TestCentsValue_UnmarshalStringAndNumber(t *testing.T) {
	for _, input := range []string{`{"val":"-1000"}`, `{"val":-1000}`} {
		var c centsValue
		if err := json.Unmarshal([]byte(input), &c); err != nil {
			t.Fatalf("Unmarshal(%s): %v", input, err)
		}
		if c.Val != -1000 {
			t.Fatalf("Unmarshal(%s) = %d, want -1000", input, c.Val)
		}
	}
}

func TestFetchUsage_UsesConfiguredTeamID(t *testing.T) {
	var sawValidation bool
	var sawBalance bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer mgmt-key" {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/auth/management-keys/validation":
			sawValidation = true
			t.Fatal("validation endpoint should not be called when team id is configured")
		case "/v1/billing/teams/team-123/prepaid/balance":
			sawBalance = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"changes":[{"topupStatus":"SUCCEEDED","amount":{"val":"-1000"}}],"total":{"val":"-250"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{
		APIKey: "mgmt-key",
		Extra:  map[string]interface{}{"team_id": "team-123"},
	})
	p.baseURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if !sawBalance || sawValidation {
		t.Fatalf("sawBalance=%v sawValidation=%v", sawBalance, sawValidation)
	}
	if got := data.Windows[0].Utilization; got != 75 {
		t.Fatalf("utilization = %.2f, want 75", got)
	}
}

func TestFetchUsage_DiscoversTeamIDFromManagementKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/management-keys/validation":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"SCOPE_TEAM","scopeId":"team-abc"}`))
		case "/v1/billing/teams/team-abc/prepaid/balance":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"changes":[{"topupStatus":"SUCCEEDED","amount":{"val":"-1000"}}],"total":{"val":"-1000"}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{APIKey: "mgmt-key"})
	p.baseURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if got := data.Windows[0].Utilization; got != 0 {
		t.Fatalf("utilization = %.2f, want 0", got)
	}
}

func TestFetchUsage_UnauthorizedReturnsExpiredData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{APIKey: "bad-key"})
	p.baseURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if !data.IsExpired {
		t.Fatal("unauthorized response should mark data expired")
	}
	if data.Error == "" {
		t.Fatal("unauthorized response should include an error")
	}
}

func TestSetupStatus_EnvManagementKey(t *testing.T) {
	t.Setenv("XAI_MANAGEMENT_API_KEY", "key")
	p := New(config.ProviderConfig{})
	if !p.IsConfigured() {
		t.Fatal("provider should be configured from XAI_MANAGEMENT_API_KEY")
	}
	if !p.SetupStatus().IsReady() {
		t.Fatalf("SetupStatus = %#v, want ready", p.SetupStatus())
	}
}

func TestConfiguredTeamID_FromEnv(t *testing.T) {
	t.Setenv("XAI_TEAM_ID", "team-env")
	p := New(config.ProviderConfig{})
	got, err := p.configuredTeamID()
	if err != nil {
		t.Fatalf("configuredTeamID: %v", err)
	}
	if got != "team-env" {
		t.Fatalf("team id = %q, want team-env", got)
	}
}

func TestManagementKey_DoesNotUseInferenceAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "inference-key")
	p := New(config.ProviderConfig{})
	if _, err := p.managementKey(); err == nil {
		t.Fatal("XAI_API_KEY must not be accepted as a management key")
	}
}

func TestSetupStatus_GrokLoginCredentials(t *testing.T) {
	writeGrokAuth(t, "grok-token", time.Now().Add(time.Hour))
	p := New(config.ProviderConfig{})

	if !p.IsConfigured() {
		t.Fatal("provider should be configured from grok login credentials")
	}
	if got := p.SetupStatus(); !got.IsReady() {
		t.Fatalf("SetupStatus = %#v, want ready", got)
	}
}

func TestAutoPollByDefault_GrokLoginCredentials(t *testing.T) {
	writeGrokAuth(t, "grok-token", time.Now().Add(time.Hour))
	p := New(config.ProviderConfig{})

	if !p.AutoPollByDefault() {
		t.Fatal("grok login credentials should auto-poll by default")
	}
}

func TestAutoPollByDefault_ManagementKeyOnlyIsOptIn(t *testing.T) {
	t.Setenv("XAI_MANAGEMENT_API_KEY", "key")
	t.Setenv("GROK_HOME", t.TempDir())
	p := New(config.ProviderConfig{})

	if p.AutoPollByDefault() {
		t.Fatal("management API keys should remain opt-in")
	}
}

func TestFetchUsage_UsesGrokLoginBilling(t *testing.T) {
	reset := time.Now().Add(6 * 24 * time.Hour).Truncate(time.Second)
	writeGrokAuth(t, "grok-token", time.Now().Add(time.Hour))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer grok-token" {
			t.Fatalf("Authorization = %q", got)
		}
		body := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if body[0] != 0 || body[1] != 0 || body[2] != 0 || body[3] != 0 || body[4] != 0 {
			t.Fatalf("request body = %v, want empty grpc-web frame", body)
		}
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grokGRPCWebResponse(grokBillingPayload(42.5, reset)))
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{})
	p.grokBillingURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if len(data.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(data.Windows))
	}
	w := data.Windows[0]
	if w.Name != "Build Weekly" {
		t.Fatalf("window name = %q, want Build Weekly", w.Name)
	}
	if w.DisplayName != "Build Weekly" {
		t.Fatalf("window display name = %q, want Build Weekly", w.DisplayName)
	}
	if math.Abs(w.Utilization-42.5) > 0.001 {
		t.Fatalf("utilization = %.3f, want 42.5", w.Utilization)
	}
	if !w.ResetsAt.Equal(reset) {
		t.Fatalf("resets_at = %s, want %s", w.ResetsAt, reset)
	}
}

func TestParseGrokBilling_NoUsageYet(t *testing.T) {
	reset := time.Now().Add(6 * 24 * time.Hour).Truncate(time.Second)
	snapshot, err := parseGrokBilling(grokGRPCWebResponse(grokBillingNoUsagePayload(reset)), time.Now())
	if err != nil {
		t.Fatalf("parseGrokBilling: %v", err)
	}
	if snapshot.UsedPercent != 0 {
		t.Fatalf("used percent = %.2f, want 0", snapshot.UsedPercent)
	}
	if !snapshot.ResetsAt.Equal(reset) {
		t.Fatalf("resets_at = %s, want %s", snapshot.ResetsAt, reset)
	}
}

func TestFetchUsage_GrokAuthRejectedReturnsExpiredData(t *testing.T) {
	writeGrokAuth(t, "grok-token", time.Now().Add(time.Hour))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		w.Header().Set("grpc-status", "16")
		w.Header().Set("grpc-message", "Authentication required")
	}))
	defer srv.Close()

	p := New(config.ProviderConfig{})
	p.grokBillingURL = srv.URL
	p.client = srv.Client()

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if !data.IsExpired {
		t.Fatal("auth rejection should mark data expired")
	}
	if data.Error == "" {
		t.Fatal("auth rejection should include an error")
	}
}

func TestGrokCredentials_Expired(t *testing.T) {
	writeGrokAuth(t, "grok-token", time.Now().Add(-time.Minute))
	p := New(config.ProviderConfig{})

	if p.IsConfigured() {
		t.Fatal("expired grok auth should not configure provider")
	}
	if got := p.SetupStatus(); got.State != "needs_auth" {
		t.Fatalf("SetupStatus = %#v, want needs_auth", got)
	}
}

func writeGrokAuth(t *testing.T, token string, expiresAt time.Time) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GROK_HOME", dir)
	data := map[string]map[string]string{
		"https://auth.x.ai::client": {
			"key":        token,
			"expires_at": expiresAt.UTC().Format(time.RFC3339Nano),
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
}

func grokGRPCWebResponse(payload []byte) []byte {
	dataFrame := grpcFrame(0, payload)
	trailerFrame := grpcFrame(0x80, []byte("grpc-status: 0\r\n"))
	return append(dataFrame, trailerFrame...)
}

func grpcFrame(flags byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flags
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func grokBillingPayload(percent float32, reset time.Time) []byte {
	var msg []byte
	msg = append(msg, protoFixed32(1, math.Float32bits(percent))...)
	msg = append(msg, protoMessage(5, protoVarint(1, uint64(reset.Unix())))...)
	msg = append(msg, protoMessage(8, protoVarint(1, 2))...)
	return protoMessage(1, msg)
}

func grokBillingNoUsagePayload(reset time.Time) []byte {
	var msg []byte
	msg = append(msg, protoMessage(4, protoVarint(1, uint64(reset.Add(-7*24*time.Hour).Unix())))...)
	msg = append(msg, protoMessage(5, protoVarint(1, uint64(reset.Unix())))...)
	msg = append(msg, protoMessage(8, protoVarint(1, 2))...)
	return protoMessage(1, msg)
}

func protoMessage(field uint64, payload []byte) []byte {
	out := protoKey(field, 2)
	out = append(out, encodeVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

func protoVarint(field, value uint64) []byte {
	out := protoKey(field, 0)
	return append(out, encodeVarint(value)...)
}

func protoFixed32(field uint64, value uint32) []byte {
	out := protoKey(field, 5)
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, value)
	return append(out, buf...)
}

func protoKey(field, wire uint64) []byte {
	return encodeVarint(field<<3 | wire)
}

func encodeVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}
