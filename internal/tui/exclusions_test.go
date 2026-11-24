package tui
import(
 "path/filepath"
 "testing"
 "strings"
 tea "github.com/charmbracelet/bubbletea"
 "github.com/fernandocpaz/tailg/internal/core"
)
func TestWindowExclusionSelectionAndPersistence(t *testing.T){
 path:=filepath.Join(t.TempDir(),"a.json")
 state:=core.NewFilterState(100)
 state.Append("routine heartbeat","important error")
 m:=model{config:Config{ExclusionFile:path},state:state,exclusions:newExclusionState(path)}
 m.exclusions.selection=&exclusionSelection{rows:[]string{"  routine heartbeat"},row:0,anchor:2,end:9}
 next,_:=m.openExclusions();m=next.(model)
 if !m.exclusions.open||state.VisibleCount()!=1||len(m.exclusions.patterns)!=1||m.exclusions.patterns[0]!="routine"{t.Fatalf("selection not applied: %+v",m.exclusions.patterns)}
 if len(newExclusionState(path).patterns)!=1{t.Fatal("not persisted")}
 if len(newExclusionState(filepath.Join(filepath.Dir(path),"b.json")).patterns)!=0{t.Fatal("scope leaked")}
 next,_=m.updateExclusions(tea.KeyMsg{Type:tea.KeyDelete});m=next.(model)
 if state.VisibleCount()!=2||len(newExclusionState(path).patterns)!=0{t.Fatal("removal not restored")}
}
func TestWindowExclusionsHistoricalAndLive(t *testing.T){
 state:=core.NewFilterState(100)
 state.Append("routine message","important message")
 state.SetExclusions([]string{"routine"})
 state.SetFilter("message")
 state.Append("routine message live")
 if state.VisibleCount()!=1||len(state.AllRecords())!=3{t.Fatal("exclusion lost or records deleted")}
 state.SetExclusions(nil)
 if state.VisibleCount()!=3{t.Fatal("removal did not restore history")}
}
func TestExclusionSelectionWideText(t *testing.T){
 s:=&exclusionSelection{rows:[]string{"  café 日本 error"},row:0,anchor:7,end:11}
 if !strings.Contains(selectedExclusion(s),"日本"){t.Fatalf("wrong selection: %q",selectedExclusion(s))}
}
