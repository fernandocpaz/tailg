package tui

import (
 "strings"
 "testing"
 "time"
)
func TestLoadingLabelFrames(t *testing.T) {
 now:=time.Unix(0,0)
 seen:=map[string]bool{}
 for i:=0;i<4;i++{
  s:=loadingLabelAt("Loading logs…",now.Add(time.Duration(i)*250*time.Millisecond))
  if !strings.HasSuffix(s,"Loading logs…"){t.Fatalf("missing label: %q",s)}
  if seen[s]{t.Fatalf("repeated frame: %q",s)}
  seen[s]=true
 }
 if loadingLabelAt("Loading logs…",now)!=loadingLabelAt("Loading logs…",now.Add(time.Second)){t.Fatal("cycle did not repeat")}
}
func TestExternalMonitorShowsBusyOnRefresh(t *testing.T){
 m:=pickerModel{external:true}
 m.monitorEnabled=true
 m.monitorBusy=true
 m.monitorLastScan=time.Now()
 if !strings.Contains(m.externalMetricsSummary(),"loading logs and CPU/memory"){t.Fatal("refresh busy state hidden")}
 m.monitorBusy=false
 if strings.Contains(m.externalMetricsSummary(),"loading"){t.Fatal("animation persisted after completion")}
}
