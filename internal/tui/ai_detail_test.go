package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/ai"
)

func TestAIHotkeyPreservesOriginalLogAndTracksUsage(t *testing.T) {
	calls:=0
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		calls++
		fmt.Fprint(w,`{"choices":[{"message":{"content":"Check the application key and upstream OAuth permissions."}}],"usage":{"prompt_tokens":90,"completion_tokens":30}}`)
	}))
	defer server.Close()
	client,err:=ai.New(ai.Config{APIKey:"test",Model:"model",BaseURL:server.URL,InputUSDPerMillion:1,OutputUSDPerMillion:2,BudgetUSD:1})
	if err!=nil {t.Fatal(err)}
	m:=workflowModel()
	m.width,m.height=100,20
	m.ctx=context.Background()
	m.config.AI=client
	original:="ERROR [09:21:35.286] [health check] GetTokenWithApplicationKey responded 401"
	m.openDetail(original)
	updated,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
	m=updated.(model)
	if cmd==nil || !m.aiView || !m.aiLoading {t.Fatalf("AI analysis was not scheduled: %+v",m)}
	updated,_=m.Update(cmd())
	m=updated.(model)
	if calls!=1 || !m.aiView || m.aiLoading || !strings.Contains(m.View(),"upstream OAuth") || !strings.Contains(m.View(),"Session: 1 calls") {
		t.Fatalf("AI analysis missing: %s",m.View())
	}
	if m.detail!=original {t.Fatalf("original log changed: %q",m.detail)}
	updated,_=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
	m=updated.(model)
	if m.aiView || !strings.Contains(m.View(),original){t.Fatalf("log was not restored: %s",m.View())}
	updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
	m=updated.(model)
	if cmd!=nil || calls!=1 {t.Fatal("reopening cached result caused a paid call")}
	updated,_=m.Update(tea.KeyMsg{Type:tea.KeyEsc})
	m=updated.(model)
	if m.aiView || m.detail=="" {t.Fatal("escape should return to log before closing detail")}
	updated,_=m.Update(tea.KeyMsg{Type:tea.KeyEsc})
	m=updated.(model)
	if m.detail!="" {t.Fatal("second escape should close inspector")}
}

func TestAIGenerationIgnoresStaleResponses(t *testing.T){
	m:=workflowModel()
	m.openDetail("ERR selected")
	m.aiView=true
	m.aiLoading=true
	m.aiGeneration=22
	m.closeDetail()
	m.openDetail("ERR newer")
	updated,_:=m.Update(aiExplanationMsg{generation:22,result:ai.Result{Explanation:"stale"}})
	m=updated.(model)
	if m.aiView || m.aiText!="" || m.detail!="ERR newer" {t.Fatalf("stale AI response corrupted new log: %+v",m)}
}

func TestAIMissingConfigurationDoesNotCallProvider(t *testing.T){
	m:=workflowModel()
	m.openDetail("ERROR some log")
	updated,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
	m=updated.(model)
	if cmd!=nil || m.aiSetup==nil || m.aiView {t.Fatal("missing AI configuration should start the setup wizard without calling a provider")}
}
