package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testConfigDir(t *testing.T) string {
	t.Helper()
	root:=t.TempDir()
	t.Setenv("XDG_CONFIG_HOME",root)
	t.Setenv("APPDATA",root)
	return root
}

func TestWizardSettingsAreSavedOwnerOnlyAndLoadedAutomatically(t *testing.T) {
	root:=testConfigDir(t)
	repo:=t.TempDir()
	initial:=Config{APIKey:"test-secret-private",Model:"gpt-test",BaseURL:"https://api.openai.com/v1",Repos:[]string{repo},InputUSDPerMillion:0.2,OutputUSDPerMillion:0.5,BudgetUSD:1}
	if err:=SaveSettings(initial);err!=nil{t.Fatal(err)}
	path,err:=SettingsPath()
	if err!=nil{t.Fatal(err)}
	if !strings.HasPrefix(path,root){t.Fatalf("config outside of test root: %s",path)}
	info,err:=os.Stat(path)
	if err!=nil{t.Fatal(err)}
	if runtime.GOOS!="windows" && info.Mode().Perm()!=0600{t.Fatalf("key file permissions: %o",info.Mode().Perm())}
	got,ok,err:=LoadSettings()
	if err!=nil || !ok || got.APIKey!=initial.APIKey || got.Model!=initial.Model || len(got.Repos)!=1 || got.BudgetUSD!=1{
		t.Fatalf("settings roundtrip: ok=%v err=%v model=%q",ok,err,got.Model)
	}
	t.Setenv("TAILG_AI_MODEL","old-model")
	t.Setenv("TAILG_AI_API_KEY","old-key")
	merged,err:=EnvConfig(nil)
	if err!=nil{t.Fatal(err)}
	if merged.APIKey!=initial.APIKey || merged.Model!=initial.Model{t.Fatal("wizard settings did not supersede stale env variables")}
	next:=initial
	next.Model="updated-model"
	if err:=SaveSettings(next);err!=nil{t.Fatal(err)}
	back,_,err:=LoadSettings()
	if err!=nil || back.Model!="updated-model" {t.Fatal("settings update did not persist")}
}

func TestStoredSettingsRejectUnsafePermissionsAndSymlinks(t *testing.T){
	testConfigDir(t)
	cfg:=Config{APIKey:"key",Model:"model"}
	if err:=SaveSettings(cfg);err!=nil{t.Fatal(err)}
	path,_:=SettingsPath()
	if runtime.GOOS!="windows"{
		if err:=os.Chmod(path,0644);err!=nil{t.Fatal(err)}
		if _,_,err:=LoadSettings();err==nil{t.Fatal("world-readable API key file accepted")}
		if err:=os.Chmod(path,0600);err!=nil{t.Fatal(err)}
	}
	if err:=os.Remove(path);err!=nil{t.Fatal(err)}
	target:=filepath.Join(t.TempDir(),"sensitive.json")
	_ = os.WriteFile(target,[]byte("not settings"),0600)
	if err:=os.Symlink(target,path);err!=nil{
		if runtime.GOOS=="windows"{t.Skip("symlink privilege unavailable on Windows")}
		t.Fatal(err)
	}
	if _,_,err:=LoadSettings();err==nil{t.Fatal("symlink to credential store accepted")}
	if err:=SaveSettings(cfg);err==nil{t.Fatal("symlink destination was overwritten")}
}

func TestProviderCheckHandlesWorkingKeyUnauthorizedAndQuota(t *testing.T){
	status:=http.StatusOK
	requests:=0
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		requests++
		if r.Header.Get("Authorization")!="Bearer secret"{t.Error("missing auth header")}
		w.WriteHeader(status)
		if status==http.StatusOK{fmt.Fprint(w,`{"choices":[{"message":{"content":"OK"}}]}`)}
	}))
	defer server.Close()
	cfg:=Config{APIKey:"secret",Model:"unit-model",BaseURL:server.URL}
	client,err:=New(cfg)
	if err!=nil{t.Fatal(err)}
	if err:=client.Check(context.Background());err!=nil{t.Fatal(err)}
	if requests!=1{t.Fatalf("calls=%d",requests)}
	status=http.StatusUnauthorized
	if err:=client.Check(context.Background());err==nil || !strings.Contains(err.Error(),"API key"){t.Fatalf("expected key failure, got %v",err)}
	status=http.StatusTooManyRequests
	if err:=client.Check(context.Background());err==nil || !strings.Contains(err.Error(),"quota"){t.Fatalf("expected rate failure, got %v",err)}
}
