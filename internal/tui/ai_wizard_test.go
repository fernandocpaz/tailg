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

func wizardKey(m model,key string) (model,tea.Cmd) {
	var message tea.KeyMsg
	switch key {
	case "enter": message=tea.KeyMsg{Type:tea.KeyEnter}
	case "esc": message=tea.KeyMsg{Type:tea.KeyEsc}
	case "space": message=tea.KeyMsg{Type:tea.KeySpace}
	case "shift+tab": message=tea.KeyMsg{Type:tea.KeyShiftTab}
	default:message=tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune(key)}
	}
	result,command:=m.Update(message)
	return result.(model),command
}

func TestUnconfiguredAOpensMaskedWizardAndCancelDoesNotPersist(t *testing.T) {
	root:=t.TempDir()
	t.Setenv("XDG_CONFIG_HOME",root)
	t.Setenv("APPDATA",root)
	m:=workflowModel()
	m.width,m.height=112,29
	m.openDetail("ERROR selected")
	m,command:=wizardKey(m,"a")
	if command!=nil || m.aiSetup==nil || m.aiView {t.Fatal("A should open setup before API call")}
	if !strings.Contains(m.View(),"AI SETUP"){t.Fatal("wizard modal missing")}
	m,command=wizardKey(m,"enter")
	if command!=nil || m.aiSetup.step!=aiStepKey{t.Fatal("provider next failed")}
	m.aiSetup.input.SetValue("SENSITIVE_SECRET_SHOULD_NOT_RENDER")
	if strings.Contains(m.View(),"SENSITIVE_SECRET_SHOULD_NOT_RENDER"){t.Fatal("API key visible in wizard")}
	m,_=wizardKey(m,"esc")
	if m.aiSetup!=nil || m.detail==""{t.Fatal("cancel should return to error")}
	if _,exists,err:=ai.LoadSettings();exists || err!=nil{t.Fatal("cancellation wrote credentials")}
}

func TestWizardValidatesSavesAndUsesProviderImmediately(t *testing.T){
	root:=t.TempDir()
	t.Setenv("XDG_CONFIG_HOME",root)
	t.Setenv("APPDATA",root)
	queries:=0
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		queries++
		if r.Header.Get("Authorization")!="Bearer key-demo"{t.Errorf("bad auth")}
		w.Header().Set("Content-Type","application/json")
		fmt.Fprint(w,`{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
	}))
	defer server.Close()
	m:=workflowModel()
	m.ctx=context.Background()
	m.width,m.height=120,30
	m.openDetail("ERROR selected")
	updatedModel,_:=m.openAIWizard("")
	m=updatedModel.(model)
	w:=m.aiSetup
	w.provider=1
	w.fields[aiStepKey]="key-demo"
	w.fields[aiStepModel]="compatible-model"
	w.fields[aiStepEndpoint]=server.URL
	w.step=aiStepTest
	m,cmd:=wizardKey(m,"enter")
	if cmd==nil || !w.busy{t.Fatal("test was not started")}
	result:=cmd()
	m=m.UpdateModelTest(result)
	if w.step!=aiStepSave || w.verified==nil{t.Fatalf("successful validation did not reach Save: %s",w.note)}
	m,cmd=wizardKey(m,"enter")
	if m.aiSetup!=nil || m.config.AI==nil || cmd==nil{t.Fatal("Save should persist settings and start explanation")}
	if queries!=1{t.Fatalf("expected only validation call before saving, got %d",queries)}
	stored,ok,err:=ai.LoadSettings()
	if err!=nil || !ok || stored.APIKey!="key-demo" || stored.BaseURL!=server.URL{t.Fatalf("settings not saved: %v %v",ok,err)}
	updated,_:=m.Update(cmd())
	m=updated.(model)
	if queries!=2 || !m.aiView || !strings.Contains(m.aiText,"OK"){t.Fatalf("not immediately usable: calls %d, text %q",queries,m.aiText)}
}

func (m model) UpdateModelTest(msg tea.Msg) model {
	updated,_:=m.Update(msg)
	return updated.(model)
}

func TestWizardFailedProviderStaysEditableAndSavesNothing(t *testing.T){
	root:=t.TempDir()
	t.Setenv("XDG_CONFIG_HOME",root)
	t.Setenv("APPDATA",root)
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	m:=workflowModel()
	m.width,m.height=112,29
	m.ctx=context.Background()
	m.openDetail("ERROR selected")
	updatedModel,_:=m.openAIWizard("credentials expired")
	m=updatedModel.(model)
	w:=m.aiSetup
	w.provider=1
	w.fields[aiStepKey]="bad-key"
	w.fields[aiStepModel]="model"
	w.fields[aiStepEndpoint]=server.URL
	w.step=aiStepTest
	m,cmd:=wizardKey(m,"enter")
	if cmd==nil{t.Fatal("validation not launched")}
	updated,_:=m.Update(cmd())
	m=updated.(model)
	if w.step!=aiStepTest || w.verified!=nil || !strings.Contains(w.note,"Validation failed"){
		t.Fatalf("failed connection not editable: %q",w.note)
	}
	m,_=wizardKey(m,"shift+tab")
	if w.step!=aiStepBudget{t.Fatal("back navigation missing")}
	if _,exists,err:=ai.LoadSettings();exists || err!=nil{t.Fatal("failed validation persisted key")}
}

func TestRepoStepSupportsSelectingAndAddingFolders(t *testing.T){
	m:=workflowModel()
	m.width,m.height=120,29
	m.openDetail("ERROR selected")
	updatedModel,_:=m.openAIWizard("")
	m=updatedModel.(model)
	w:=m.aiSetup
	w.step=aiStepRepositories
	path:=t.TempDir()
	m,_=wizardKey(m,"a")
	w.input.SetValue(path)
	m,_=wizardKey(m,"enter")
	if w.addRepo || !w.selected[path]{t.Fatal("folder not selected")}
	cfg,err:=w.buildConfig()
	if err!=nil || len(cfg.Repos)==0{t.Fatalf("repo configuration missing: %+v, err %v",cfg.Repos,err)}
}

