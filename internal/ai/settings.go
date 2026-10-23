package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SettingsPath is in the OS user configuration directory, never the checkout.
// WARNING: the secret is stored in a user-restricted local file, not OS keychain.
func SettingsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil { return "", fmt.Errorf("locate user settings: %w", err) }
	return filepath.Join(base, "tailg", "ai.json"), nil
}

type savedSettings struct {
	APIKey string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model string `json:"model"`
	Repos []string `json:"repos,omitempty"`
	BudgetUSD float64 `json:"budget_usd,omitempty"`
	InputUSDPerMillion float64 `json:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion float64 `json:"output_usd_per_million,omitempty"`
}

func LoadSettings() (Config, bool, error) {
	path, err := SettingsPath()
	if err != nil { return Config{}, false, err }
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) { return Config{}, false, nil }
	if err != nil { return Config{}, false, err }
	if !info.Mode().IsRegular() { return Config{}, false, errors.New("AI settings must be a regular file") }
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return Config{}, false, errors.New("AI settings permissions are too broad (file must be owner-only)")
	}
	if info.Size() > 64*1024 { return Config{}, false, errors.New("AI settings file is unexpectedly large") }
	file, err := os.Open(path)
	if err != nil { return Config{}, false, err }
	defer file.Close()
	var saved savedSettings
	decoder := json.NewDecoder(io.LimitReader(file,64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil { return Config{}, false, fmt.Errorf("read AI settings: %w",err) }
	var trailing any
	if decoder.Decode(&trailing) != io.EOF { return Config{}, false, errors.New("unexpected data after AI settings") }
	return Config{
		APIKey:saved.APIKey, BaseURL:saved.BaseURL, Model:saved.Model, Repos:saved.Repos,
		BudgetUSD:saved.BudgetUSD, InputUSDPerMillion:saved.InputUSDPerMillion,
		OutputUSDPerMillion:saved.OutputUSDPerMillion,
	},true,nil
}

func SaveSettings(cfg Config) error {
	// Validate URLs, model and local repository roots first. Do not persist
	// unverified or invalid connection parameters.
	if _,err:=New(cfg);err!=nil{return err}
	path,err:=SettingsPath()
	if err!=nil{return err}
	dir:=filepath.Dir(path)
	if err=os.MkdirAll(dir,0700);err!=nil{return err}
	d,err:=os.Lstat(dir)
	if err!=nil{return err}
	if !d.IsDir() || d.Mode()&os.ModeSymlink!=0{return errors.New("AI settings directory must not be a symlink")}
	if runtime.GOOS!="windows" && d.Mode().Perm()&0022!=0{return errors.New("AI settings directory is writable by other users")}
	if existing,e:=os.Lstat(path);e==nil {
		if !existing.Mode().IsRegular(){return errors.New("AI settings destination is not a regular file")}
	} else if !errors.Is(e,os.ErrNotExist){return e}
	payload,err:=json.MarshalIndent(savedSettings{
		APIKey:cfg.APIKey,BaseURL:cfg.BaseURL,Model:cfg.Model,Repos:cfg.Repos,
		BudgetUSD:cfg.BudgetUSD,InputUSDPerMillion:cfg.InputUSDPerMillion,
		OutputUSDPerMillion:cfg.OutputUSDPerMillion,
	},"","  ")
	if err!=nil{return err}
	temp,err:=os.CreateTemp(dir,".ai-*.tmp")
	if err!=nil{return err}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err=temp.Chmod(0600);err!=nil{return err}
	if _,err=temp.Write(append(payload,'\n'));err!=nil{return err}
	if err=temp.Sync();err!=nil{return err}
	if err=temp.Close();err!=nil{return err}
	if err=os.Rename(temp.Name(),path);err!=nil{return fmt.Errorf("save AI settings: %w",err)}
	return nil
}

// Check proves that the configured model and credentials work, with a tiny
// harmless chat request. It sends NO application log or repository content.
// The validation request can still count against API quota/cost.
func (c *Client) Check(ctx context.Context) error {
	if c==nil{return errors.New("AI provider is not configured")}
	body,_:=json.Marshal(struct {
		Model string `json:"model"`
		Messages []chatMessage `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}{Model:c.cfg.Model, Messages:[]chatMessage{{Role:"user",Content:"Reply OK."}},MaxTokens:8})
	request,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.endpoint,strings.NewReader(string(body)))
	if err!=nil{return err}
	request.Header.Set("Content-Type","application/json")
	request.Header.Set("Authorization","Bearer "+c.cfg.APIKey)
	response,err:=c.http.Do(request)
	if err!=nil{return fmt.Errorf("cannot reach provider: %w",err)}
	defer response.Body.Close()
	if response.StatusCode!=http.StatusOK {
		switch response.StatusCode {
		case http.StatusUnauthorized,http.StatusForbidden:return errors.New("provider rejected API key or account access (HTTP 401/403)")
		case http.StatusTooManyRequests:return errors.New("provider rate limit or billing quota reached (HTTP 429)")
		case http.StatusNotFound:return errors.New("provider endpoint or model was not found (HTTP 404)")
		default:return fmt.Errorf("provider returned HTTP %d; verify endpoint, model, and account",response.StatusCode)
		}
	}
	var completion chatResponse
	if err=json.NewDecoder(io.LimitReader(response.Body,4096)).Decode(&completion);err!=nil{return errors.New("provider response is not compatible with Chat Completions")}
	if len(completion.Choices)==0 {return errors.New("provider returned no chat choices")}
	return nil
}

// DiscoverRepos offers likely local checkouts to nontechnical users without
// executing git commands, following symlinks, or traversing entire disks.
func DiscoverRepos() []string {
	var roots []string
	if wd,err:=os.Getwd();err==nil {roots=append(roots,wd)}
	if home,err:=os.UserHomeDir();err==nil {
		for _,name:=range []string{"src","source","repos","projects","code","dev","workspace"} {
			roots=append(roots,filepath.Join(home,name))
		}
	}
	var found []string
	seen:=map[string]bool{}
	add:=func(path string){
		abs,e:=filepath.Abs(path)
		if e!=nil || seen[abs] || len(found)>=16{return}
		seen[abs]=true
		info,e:=os.Lstat(abs)
		if e!=nil || !info.IsDir() || info.Mode()&os.ModeSymlink!=0{return}
		git,e:=os.Lstat(filepath.Join(abs,".git"))
		if e==nil && git.Mode()&os.ModeSymlink==0{found=append(found,abs)}
	}
	for _,root:=range roots {
		add(root)
		children,e:=os.ReadDir(root)
		if e!=nil{continue}
		for _,child:=range children {
			if len(found)>=16{break}
			if child.IsDir() && !strings.HasPrefix(child.Name(),"."){add(filepath.Join(root,child.Name()))}
		}
	}
	return found
}

var _ = time.Second
