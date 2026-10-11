package cli

import (
 "encoding/json"
 "testing"
 "time"

 "github.com/tnunamak/clawmeter/internal/provider"
)

// This helper-level test requires statusCheckExitCode from the fix.
// The existing-entry-point regression above proves the classification on base.
func TestE1CheckExitCodes(t *testing.T) {
 for _,tt:=range []struct{name string; windowJSON string; remaining time.Duration; want int}{
  {"monthly",`{"name":"premium","utilization":60,"window_length_seconds":2592000}`,12*time.Hour,0},
  {"early",`{"name":"5h","utilization":3}`,298*time.Minute,0},
  {"expired",`{"name":"5h","utilization":100}`,-5*time.Hour,1},
  {"unknown reset",`{"name":"5h","utilization":100}`,0,2},
  {"unknown length",`{"name":"mystery","utilization":95}`,time.Hour,0},
 } {
  t.Run(tt.name,func(t *testing.T){
   var w provider.UsageWindow
   if err:=json.Unmarshal([]byte(tt.windowJSON),&w);err!=nil {t.Fatal(err)}
   if tt.remaining!=0 {w.ResetsAt=time.Now().Add(tt.remaining)}
   output:=&MultiProviderOutput{Providers:[]ProviderFormatter{{Data:&provider.UsageData{Windows:[]provider.UsageWindow{w}}}}}
   if got:=statusCheckExitCode(output);got!=tt.want {t.Errorf("exit=%d want %d",got,tt.want)}
  })
 }
}
