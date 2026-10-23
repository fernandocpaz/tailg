package tui

import (
 "context"
 "fmt"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"

 tea "github.com/charmbracelet/bubbletea"
 "github.com/fernandocpaz/tailg/internal/ai"
 "github.com/fernandocpaz/tailg/internal/core"
)

func TestServiceRepoPromptPersistsMappingAndRestrictsSource(t *testing.T) {
 t.Setenv("XDG_CONFIG_HOME",t.TempDir())
 t.Setenv("APPDATA",t.TempDir())
 root:=t.TempDir()
 if err:=os.WriteFile(filepath.Join(root,"RentalResultsAuthService.cs"),
  []byte("public class RentalResultsAuthService { void GetTokenWithApplicationKey() {} }"),0600);err!=nil{t.Fatal(err)}
 requests:=0
 var payload string
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  requests++
  b:=make([]byte,40000);n,_:=r.Body.Read(b);payload=string(b[:n])
  fmt.Fprint(w,`{"choices":[{"message":{"content":"Explanation"}}],"usage":{"prompt_tokens":20,"completion_tokens":4}}`)
 }))
 defer server.Close()
 cfg:=ai.Config{APIKey:"key",Model:"model",BaseURL:server.URL}
 client,err:=ai.New(cfg);if err!=nil{t.Fatal(err)}
 m:=workflowModel()
 m.width,m.height=118,25
 m.ctx=context.Background()
 m.config.Namespace="qa"
 m.config.AI=client
 m.config.AISettings=cfg
 m.openDetail("ERROR RentalResultsAuthService token failed")
 m.detailRecord=&core.LogRecord{Event:core.LogEvent{Workload:"rental-service",Pod:"rental-service-aaa999aa9-xxxyy"}}
 updated,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
 m=updated.(model)
 if cmd!=nil||requests!=0||m.serviceRepoSetup==nil||m.serviceRepoSetup.service!="qa/rental-service"{
  t.Fatal("missing mapping must prompt BEFORE sending source or logs")
 }
 updated,_=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
 m=updated.(model)
 if m.serviceRepoSetup.mode!="path"{t.Fatal("local repo path not offered")}
 m.serviceRepoSetup.input.SetValue(root)
 updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyEnter})
 m=updated.(model)
 if m.serviceRepoSetup!=nil||cmd==nil||!m.aiView{t.Fatal("saved mapping must start explanation")}
 if got,ok:=m.config.AISettings.CurrentServiceRepo("qa/rental-service");!ok||got.Path!=root{t.Fatal("wrong saved service path")}
 updated,_=m.Update(cmd())
 m=updated.(model)
 if requests!=1||!strings.Contains(payload,"RentalResultsAuthService.cs"){t.Fatal("AI was not given matching source from selected service checkout")}
 saved,exists,err:=ai.LoadSettings()
 if err!=nil||!exists||saved.ServiceRepos["qa/rental-service"].Path!=root{t.Fatal("service mapping not persisted")}
 m.closeDetail()
 m.openDetail("ERROR OtherComponent")
 m.detailRecord=&core.LogRecord{Event:core.LogEvent{Workload:"other-service",Pod:"other-service-123456789-aaabb"}}
 updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
 m=updated.(model)
 if cmd!=nil||m.serviceRepoSetup==nil||requests!=1{t.Fatal("unrelated service must prompt independently")}
 updated,cmd=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'l'}})
 m=updated.(model)
 if cmd==nil||!m.aiView||m.serviceRepoSetup!=nil{t.Fatal("explicit log-only path missing")}
 updated,_=m.Update(cmd());m=updated.(model)
 if requests!=2||strings.Contains(payload,"RentalResultsAuthService.cs"){t.Fatal("log-only request leaked the previously mapped service's source")}
}

func TestServiceWorkloadAndPodFallback(t *testing.T){
 m:=workflowModel()
 m.config.Namespace="qa"
 m.openDetail("err")
 m.detailRecord=&core.LogRecord{Event:core.LogEvent{Workload:"my-service",Pod:"my-service-74cdc8485d-fdvjs"}}
 if key:=m.selectedAIService();key!="qa/my-service"{t.Fatalf("controller workload not preferred: %s",key)}
 m.detailRecord.Event.Workload=""
 if key:=m.selectedAIService();key!="qa/my-service"{t.Fatalf("pod fallback did not remove ReplicaSet suffix: %s",key)}
 m.detailRecord.Event.Pod=""
 if key:=m.selectedAIService();key!=""{t.Fatalf("unknown service should not bind random repo: %q",key)}
}

func TestCancelledRepoPromptNeverSendsAIRequest(t *testing.T){
 m:=workflowModel()
 m.ctx=context.Background()
 m.config.AI=&ai.Client{}
 m.config.AISettings=ai.Config{APIKey:"key",Model:"model"}
 m.openDetail("ERR")
 m.detailRecord=&core.LogRecord{Event:core.LogEvent{Workload:"orders",Pod:"orders-abc"}}
 updated,_:=m.Update(tea.KeyMsg{Type:tea.KeyRunes,Runes:[]rune{'a'}})
 m=updated.(model)
 if m.serviceRepoSetup==nil{t.Fatal("mapping prompt missing")}
 updated,cmd:=m.Update(tea.KeyMsg{Type:tea.KeyEsc})
 m=updated.(model)
 if cmd!=nil||m.serviceRepoSetup!=nil||m.aiView{t.Fatal("cancel should return without paid request")}
}
