package tui
import(
 "testing"
 tea "github.com/charmbracelet/bubbletea"
)
func TestExternalScopeSearchAndSelection(t *testing.T){
 m:=pickerModel{scopeDialog:&externalScopeDialog{kind:"namespace",names:[]string{"demo-shop","payments","inventory"}}}
 next,_:=m.externalScopeKey(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune("pay")})
 m=next.(pickerModel)
 rows:=m.scopeDialog.choices()
 if len(rows)!=2||rows[0].value!="payments"{t.Fatalf("unexpected choices: %+v",rows)}
 next,_=m.externalScopeKey(tea.KeyMsg{Type:tea.KeyEnter})
 m=next.(pickerModel)
 if m.result.Action!=PickerSwitchScope||m.result.ScopeValue!="payments"||m.scopeDialog!=nil{t.Fatal("selection failed")}
}
func TestExternalScopeStaleResultAndAllClusters(t *testing.T){
 m:=pickerModel{scopeRequest:2,scopeDialog:&externalScopeDialog{kind:"cluster",loading:true,names:[]string{"example-production"}}}
 m.externalScopeMessage(externalScopeResult{request:1,names:[]string{"stale"}})
 if !m.scopeDialog.loading{t.Fatal("stale response applied")}
 m.externalScopeMessage(externalScopeResult{request:2,names:[]string{"example-testing"}})
 rows:=m.scopeDialog.choices()
 if len(rows)!=3||rows[2].value!=""{t.Fatalf("missing all clusters option: %+v",rows)}
}
