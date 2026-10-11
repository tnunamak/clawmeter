package forecast

import (
 "testing"
 "time"
)

func TestE1WindowNames(t *testing.T) {
 for name, want := range map[string]time.Duration{"session_5h":5*time.Hour,"search-hourly":time.Hour,"45m":45*time.Minute,"daily":24*time.Hour,"weekly":7*24*time.Hour,"monthly":30*24*time.Hour,"unknown":0} {
  if got:=GuessWindowType(name);got!=want { t.Errorf("%s: got %v want %v",name,got,want) }
 }
}
func TestE1UnknownAndStaleProjection(t *testing.T) {
 for _, tt:=range []struct{name string;pct float64;reset time.Time;length time.Duration;want string}{
  {"early",3,time.Now().Add(298*time.Minute),5*time.Hour,"unknown"},
  {"unknown length",60,time.Now().Add(12*time.Hour),0,"unknown"},
  {"past reset",100,time.Now().Add(-5*time.Hour),5*time.Hour,"stale"},
  {"unknown reset",100,time.Time{},5*time.Hour,"unknown"},
 } {
  t.Run(tt.name,func(t *testing.T){p:=Project(tt.pct,tt.reset,tt.length);if p.Indicator()!=tt.want {t.Errorf("indicator=%s want %s",p.Indicator(),tt.want)}})
 }
}

func TestE1ProjectionSampleThreshold(t *testing.T) {
 for _, tt := range []struct { name string; length, elapsed time.Duration; unknown bool } {
  {"five hours before minimum",5*time.Hour,29*time.Minute,true},
  {"five hours after minimum",5*time.Hour,31*time.Minute,false},
  {"monthly before minimum",30*24*time.Hour,59*time.Minute,true},
  {"monthly after minimum",30*24*time.Hour,61*time.Minute,false},
 } {
  t.Run(tt.name,func(t *testing.T) {
   got := Project(3,time.Now().Add(tt.length-tt.elapsed),tt.length).Indicator()
   if (got=="unknown") != tt.unknown { t.Errorf("indicator=%q unknown=%v",got,tt.unknown) }
  })
 }
}
