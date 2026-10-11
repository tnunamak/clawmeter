package cli

import (
 "encoding/json"
 "strings"
 "testing"
 "time"

 "github.com/tnunamak/clawmeter/internal/provider"
)

func TestE1TypedMonthlyWindow(t *testing.T) {
 var w provider.UsageWindow
 if err:=json.Unmarshal([]byte(`{"name":"premium","utilization":60,"window_length_seconds":2592000}`),&w);err!=nil {t.Fatal(err)}
 w.ResetsAt=time.Now().Add(12*time.Hour)
 data:=&provider.UsageData{Windows:[]provider.UsageWindow{w}}
 projection:=forecastFor(data).Windows["premium"]
 if projection.ProjectedPct<60 || projection.ProjectedPct>62 {t.Errorf("monthly projection=%v want about 61",projection.ProjectedPct)}
 b,err:=json.Marshal(w);if err!=nil {t.Fatal(err)}
 if !strings.Contains(string(b),`"window_length_seconds":2592000`) {t.Errorf("seconds contract lost: %s",b)}
}
func TestE1WindowClassification(t *testing.T) {
 for _,tt:=range []struct{name string;w provider.UsageWindow;tier int;contains string}{
  {"early",provider.UsageWindow{Name:"5h",Utilization:3,ResetsAt:time.Now().Add(298*time.Minute)},4,"projected_at_reset=unknown"},
  {"past",provider.UsageWindow{Name:"5h",Utilization:100,ResetsAt:time.Now().Add(-5*time.Hour)},3,"status=stale"},
  {"unknown reset",provider.UsageWindow{Name:"5h",Utilization:100},2,"current=100%"},
 } {
  t.Run(tt.name,func(t *testing.T){pf:=ProviderFormatter{Display:"Test",Data:&provider.UsageData{Windows:[]provider.UsageWindow{tt.w}}}
   if got:=classifyProvider(&pf).tier;got!=tt.tier {t.Errorf("tier=%d want %d",got,tt.tier)}
   output:=MultiProviderOutput{Providers:[]ProviderFormatter{pf}}
   if got:=output.AgentSummary();!strings.Contains(got,tt.contains){t.Errorf("agent=%s want %s",got,tt.contains)}
  })
 }
}

func TestE1StatusLineUnknownAndStale(t *testing.T) {
 for _, tt := range []struct { reset time.Time; want string } {
  {time.Now().Add(298*time.Minute),"unknown"},
  {time.Now().Add(-time.Hour),"stale"},
 } {
  output:=MultiProviderOutput{Providers:[]ProviderFormatter{{Display:"Test",Data:&provider.UsageData{Windows:[]provider.UsageWindow{{Name:"5h",Utilization:3,ResetsAt:tt.reset}}}}}}
  got:=output.StatusLineSummary()
  if !strings.Contains(got,tt.want)||!strings.Contains(got,"3%") {t.Errorf("status line=%s want current 3%% and %s",got,tt.want)}
 }
}
func TestE1OldWindowJSON(t *testing.T) {
 var w provider.UsageWindow
 if err:=json.Unmarshal([]byte(`{"name":"5h","utilization":10}`),&w);err!=nil {t.Fatal(err)}
 b,err:=json.Marshal(w);if err!=nil {t.Fatal(err)}
 if strings.Contains(string(b),"window_length_seconds") {t.Errorf("zero length should be omitted: %s",b)}
}
