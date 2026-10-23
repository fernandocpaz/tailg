package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainRedactsLogIncludesRelevantLocalSourcesAndReportsUsage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "RentalResultsAuthService.cs"),
		[]byte("namespace Test;\npublic class RentalResultsAuthService {\n    public void GetTokenWithApplicationKey() {\n        throw new Exception(\"unauthorized\");\n    }\n}\n"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(root, "secrets.cs"),
		[]byte("public class GetTokenWithApplicationKey { string APIKey = \"SECRET_REPO_VALUE\"; }"),0600); err != nil { t.Fatal(err) }

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost ||
			r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected API request %s %s",r.Method,r.URL)
		}
		var payload struct { Messages []struct { Content string `json:"content"` } `json:"messages"` }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { t.Error(err); return }
		if len(payload.Messages)!=2 { t.Errorf("messages=%d",len(payload.Messages));return }
		prompt:=payload.Messages[1].Content
		if strings.Contains(prompt,"secret-token-123") || strings.Contains(prompt,"SECRET_REPO_VALUE") {
			t.Error("secret leaked in prompt")
		}
		if !strings.Contains(prompt,"GetTokenWithApplicationKey") || !strings.Contains(prompt,"RentalResultsAuthService.cs") {
			t.Errorf("expected source context: %q",prompt)
		}
		w.Header().Set("Content-Type","application/json")
		w.Header().Set("X-Ratelimit-Remaining-Requests", "45")
		w.Header().Set("X-Ratelimit-Remaining-Tokens", "9000")
		fmt.Fprint(w,`{"choices":[{"message":{"content":"401 likely indicates invalid application key. Check service account configuration."}}],"usage":{"prompt_tokens":500,"completion_tokens":100,"total_tokens":600}}`)
	}))
	defer server.Close()
	client, err := New(Config{
		APIKey:"test-key", Model:"demo", BaseURL:server.URL+"/v1", Repos:[]string{root},
		InputUSDPerMillion:1, OutputUSDPerMillion:2, BudgetUSD:1,
	})
	if err != nil { t.Fatal(err) }
	result,err:=client.Explain(context.Background(),"ERROR [09:21:35.286] [health check] GetTokenWithApplicationKey Unauthorized authorization: secret-token-123")
	if err!=nil { t.Fatal(err) }
	if requests!=1 || result.RequestInputTokens!=500 || result.RequestOutputTokens!=100 || len(result.MatchedFiles)!=1 {t.Fatalf("unexpected result: %+v requests=%d",result,requests)}
	if result.RequestUSD<0.00069 || result.RequestUSD>0.00071 {t.Fatalf("cost=%f",result.RequestUSD)}
	if result.Usage.Calls!=1 || result.Usage.RemainingUSD>=1 || !strings.Contains(UsageText(result.Usage),"local session allowance left") ||
		!result.Usage.HasRateRemainingRequests || result.Usage.RateRemainingRequests != 45 ||
		!result.Usage.HasRateRemainingTokens || result.Usage.RateRemainingTokens != 9000 {
		t.Fatalf("usage=%+v",result.Usage)
	}
}

func TestAIRequiresTLSAndConfiguration(t *testing.T) {
	if _,err:=New(Config{APIKey:"key",Model:"model",BaseURL:"http://example.com/v1"});err==nil {t.Fatal("insecure remote endpoint accepted")}
	if _,err:=New(Config{APIKey:"key",Model:"model",BudgetUSD:1});err==nil {t.Fatal("unpriced budget accepted")}
	if _,err:=New(Config{APIKey:"",Model:"model"});err==nil {t.Fatal("empty key accepted")}
}

func TestAIQuotaIsNotInventedAndLowAllowanceStopsRequest(t *testing.T) {
	called:=0
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,_ *http.Request){
		called++
		fmt.Fprint(w,`{"choices":[{"message":{"content":"Explanation"}}]}`)
	}))
	defer server.Close()
	client,err:=New(Config{APIKey:"key",Model:"model",BaseURL:server.URL,BudgetUSD:0.000001,InputUSDPerMillion:100,OutputUSDPerMillion:100})
	if err!=nil {t.Fatal(err)}
	if _,err=client.Explain(context.Background(),"ERROR x");err==nil || called!=0 {t.Fatalf("budget was not checked before request: %v calls=%d",err,called)}
	client,err=New(Config{APIKey:"key",Model:"model",BaseURL:server.URL})
	if err!=nil {t.Fatal(err)}
	result,err:=client.Explain(context.Background(),"ERROR x")
	if err!=nil {t.Fatal(err)}
	if result.RequestHasTokens || result.Usage.HasTokens || result.Usage.HasPrice || result.Usage.HasBudget || !strings.Contains(UsageText(result.Usage),"provider account balance not available"){
		t.Fatalf("invented usage data %+v",result.Usage)
	}
}

func TestSourceContextOnlyReadsAllowedCodeAndBoundedFiles(t *testing.T) {
	root:=t.TempDir()
	_ = os.Mkdir(filepath.Join(root,"node_modules"),0700)
	_ = os.WriteFile(filepath.Join(root,"node_modules","AuthService.cs"),[]byte("GetTokenWithApplicationKey secret-source"),0600)
	_ = os.WriteFile(filepath.Join(root,".env"),[]byte("GetTokenWithApplicationKey=private"),0600)
	_ = os.WriteFile(filepath.Join(root,"AuthService.cs"),[]byte("class AuthService { void GetTokenWithApplicationKey() {} }"),0600)
	source,names:=sourceContext([]string{root},"GetTokenWithApplicationKey failed")
	if len(names)!=1 || strings.Contains(source,"secret-source") || strings.Contains(source,"private") {t.Fatalf("unexpected indexed code: names=%v, src=%s",names,source)}
}
