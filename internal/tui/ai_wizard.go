package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/ai"
)

// The complete setup stays inside tailg. Steps cannot send logs or source code;
// "Test" sends only "Reply OK" and "Save" writes a local, owner-restricted file.
const (
	aiStepProvider = iota
	aiStepKey
	aiStepWorkspace
	aiStepModel
	aiStepEndpoint
	aiStepRepositories
	aiStepInputRate
	aiStepOutputRate
	aiStepBudget
	aiStepTest
	aiStepSave
)

const (
	aiProviderOpenAI = iota
	aiProviderClaude
	aiProviderCompatible
)

type aiSetupWizard struct {
	step int
	provider int
	fields [aiStepTest]string
	input textinput.Model
	repos []string
	selected map[string]bool
	repoIndex int
	addRepo bool
	note string
	busy bool
	generation int
	cancel context.CancelFunc
	verified *ai.Client
	validatedConfig ai.Config
}

type aiSetupTestMsg struct {
	generation int
	client *ai.Client
	config ai.Config
	err error
}

func wizardModel(cfg ai.Config, reason string) *aiSetupWizard {
	w := &aiSetupWizard{selected:map[string]bool{}, note:reason}
	w.provider=aiProviderOpenAI
	if strings.EqualFold(cfg.Provider,ai.ProviderAnthropic) || strings.EqualFold(cfg.Provider,"claude") {
		w.provider=aiProviderClaude
	} else if cfg.BaseURL!="" && strings.TrimRight(cfg.BaseURL,"/")!="https://api.openai.com/v1" {w.provider=aiProviderCompatible}
	w.fields[aiStepKey]=cfg.APIKey
	w.fields[aiStepWorkspace]=cfg.WorkspaceID
	w.fields[aiStepModel]=cfg.Model
	if w.fields[aiStepModel]=="" {w.fields[aiStepModel]="gpt-4.1-mini"}
	if cfg.Model=="" && w.provider==aiProviderClaude {w.fields[aiStepModel]=ai.DefaultClaudeModel}
	w.fields[aiStepEndpoint]=cfg.BaseURL
	if w.fields[aiStepEndpoint]=="" {w.fields[aiStepEndpoint]="https://api.openai.com/v1"}
	if cfg.BaseURL=="" && w.provider==aiProviderClaude {w.fields[aiStepEndpoint]=ai.AnthropicBaseURL}
	for i,n:=range []float64{cfg.InputUSDPerMillion,cfg.OutputUSDPerMillion,cfg.BudgetUSD} {
		if n>0 {w.fields[aiStepInputRate+i]=strconv.FormatFloat(n,'f',-1,64)}
	}
	for _,root:=range append(ai.DiscoverRepos(),cfg.Repos...) {
		abs,err:=filepath.Abs(root)
		if err!=nil{continue}
		found:=false
		for _,old:=range w.repos {if old==abs{found=true;break}}
		if !found{w.repos=append(w.repos,abs)}
	}
	for _,root:=range cfg.Repos {
		if abs,err:=filepath.Abs(root);err==nil {w.selected[abs]=true}
	}
	w.input=textinput.New()
	w.input.Prompt="> "
	w.input.CharLimit=2048
	w.input.Width=55
	w.loadInput()
	return w
}

func (w *aiSetupWizard) selectProvider(provider int) {
	if w.provider==provider{return}
	w.provider=provider
	// A provider switch must not send an old provider's key or use its prices.
	w.fields[aiStepKey]=""
	w.fields[aiStepWorkspace]=""
	for i:=aiStepInputRate;i<=aiStepBudget;i++{w.fields[i]=""}
	if provider==aiProviderClaude {
		w.fields[aiStepModel]=ai.DefaultClaudeModel
		w.fields[aiStepEndpoint]=ai.AnthropicBaseURL
	} else {
		w.fields[aiStepModel]="gpt-4.1-mini"
		w.fields[aiStepEndpoint]="https://api.openai.com/v1"
		if provider==aiProviderCompatible {w.fields[aiStepModel]="";w.fields[aiStepEndpoint]=""}
	}
	w.note=""
	w.verified=nil
}

func (w *aiSetupWizard) loadInput() {
	w.addRepo=false
	if w.step<aiStepKey || w.step>aiStepBudget || w.step==aiStepRepositories{return}
	w.input.SetValue(w.fields[w.step])
	w.input.CursorEnd()
	w.input.EchoMode=textinput.EchoNormal
	if w.step==aiStepKey {w.input.EchoMode=textinput.EchoPassword;w.input.EchoCharacter='*'}
	w.input.Focus()
}

func (w *aiSetupWizard) commitInput(){
	if w.step>=aiStepKey && w.step<=aiStepBudget && w.step!=aiStepRepositories{
		w.fields[w.step]=strings.TrimSpace(w.input.Value())
	}
}
func (w *aiSetupWizard) advance() {
	w.commitInput()
	w.note=""
	w.verified=nil
	w.step++
	if w.step==aiStepWorkspace && w.provider!=aiProviderClaude {w.step=aiStepModel}
	if w.step==aiStepEndpoint && w.provider!=aiProviderCompatible {w.step=aiStepRepositories}
	w.loadInput()
}
func (w *aiSetupWizard) back(){
	w.commitInput()
	w.note=""
	w.verified=nil
	if w.step>0 {w.step--}
	if w.step==aiStepEndpoint && w.provider!=aiProviderCompatible {w.step=aiStepModel}
	if w.step==aiStepWorkspace && w.provider!=aiProviderClaude {w.step=aiStepKey}
	w.loadInput()
}
func (w *aiSetupWizard) buildConfig() (ai.Config,error) {
	cfg:=ai.Config{
		Provider:ai.ProviderOpenAI,
		APIKey:strings.TrimSpace(w.fields[aiStepKey]),
		Model:strings.TrimSpace(w.fields[aiStepModel]),
		BaseURL:"https://api.openai.com/v1",
	}
	if w.provider==aiProviderCompatible{cfg.BaseURL=strings.TrimSpace(w.fields[aiStepEndpoint])}
	if w.provider==aiProviderClaude{
		cfg.Provider=ai.ProviderAnthropic
		cfg.BaseURL=strings.TrimSpace(w.fields[aiStepEndpoint])
		cfg.WorkspaceID=strings.TrimSpace(w.fields[aiStepWorkspace])
	}
	for _,root:=range w.repos {
		if w.selected[root]{cfg.Repos=append(cfg.Repos,root)}
	}
	nums:=[]*float64{&cfg.InputUSDPerMillion,&cfg.OutputUSDPerMillion,&cfg.BudgetUSD}
	for i,field:=range nums {
		value:=strings.TrimSpace(w.fields[aiStepInputRate+i])
		if value==""{continue}
		n,err:=strconv.ParseFloat(value,64)
		if err!=nil {return cfg,fmt.Errorf("invalid amount %q: please enter a number",value)}
		*field=n
	}
	return cfg,nil
}

func (w *aiSetupWizard) stop(){
	if w.cancel!=nil{w.cancel();w.cancel=nil}
	w.generation++
	w.busy=false
}

func (m model) openAIWizard(reason string) (tea.Model, tea.Cmd) {
	if m.aiSetup!=nil {m.aiSetup.stop()}
	m.cancelAI()
	m.aiView=false
	m.aiLoading=false
	m.aiText=""
	if reason=="" && m.config.AIProblem!=""{reason=m.config.AIProblem}
	m.aiSetup=wizardModel(m.config.AISettings,reason)
	return m,nil
}

func (m model) updateAIWizardKey(msg tea.KeyMsg) (tea.Model,tea.Cmd) {
	w:=m.aiSetup
	if w==nil{return m,nil}
	key:=msg.String()
	if key=="ctrl+c" || key=="esc" {
		w.stop()
		m.aiSetup=nil
		return m,nil
	}
	if w.busy {return m,nil}
	if w.step==aiStepProvider {
		switch key {
		case "right","space","down":
			w.selectProvider((w.provider+1)%3)
		case "left","up":
			w.selectProvider((w.provider+2)%3)
		case "enter","tab":
			w.advance()
		}
		return m,nil
	}
	if w.step==aiStepRepositories {
		if w.addRepo {
			switch key {
			case "enter":
				path:=strings.TrimSpace(w.input.Value())
				if path==""{w.addRepo=false;return m,nil}
				abs,err:=filepath.Abs(path)
				if err==nil {
					var info os.FileInfo
					info,err=os.Stat(abs)
					if err==nil && !info.IsDir(){err=fmt.Errorf("path must be a directory")}
				}
				if err!=nil{w.note="Repository path is not accessible: "+err.Error();return m,nil}
				exists:=false
				for _,other:=range w.repos {if other==abs{exists=true;break}}
				if !exists{w.repos=append(w.repos,abs)}
				w.selected[abs]=true
				w.repoIndex=len(w.repos)-1
				w.addRepo=false
				w.note="Folder added. Only matched source excerpts are sent to AI."
				return m,nil
			default:
				var cmd tea.Cmd
				w.input,cmd=w.input.Update(msg)
				return m,cmd
			}
		}
		switch key{
		case "up","k":
			if w.repoIndex>0{w.repoIndex--}
		case "down","j":
			if w.repoIndex+1<len(w.repos){w.repoIndex++}
		case "space":
			if len(w.repos)>0{path:=w.repos[w.repoIndex];w.selected[path]=!w.selected[path]}
		case "a":
			w.addRepo=true
			w.input.SetValue("")
			w.input.EchoMode=textinput.EchoNormal
			w.input.Focus()
			w.note="Enter an absolute path to an existing local checkout."
		case "enter","tab":
			w.advance()
		case "shift+tab":
			w.back()
		}
		return m,nil
	}
	if w.step==aiStepTest {
		switch key{
		case "shift+tab","left","b":
			w.back()
			return m,nil
		case "enter","t":
			cfg,err:=w.buildConfig()
			if err!=nil{w.note=err.Error();return m,nil}
			client,err:=ai.New(cfg)
			if err!=nil{w.note=err.Error();return m,nil}
			ctx:=m.ctx
			if ctx==nil{ctx=context.Background()}
			ctx,cancel:=context.WithTimeout(ctx,18*time.Second)
			w.stop()
			w.generation++
			generation:=w.generation
			w.cancel=cancel
			w.busy=true
			w.note="Testing API credentials and model with a short, harmless request..."
			return m,func()tea.Msg{
				defer cancel()
				err:=client.Check(ctx)
				return aiSetupTestMsg{generation:generation,client:client,config:cfg,err:err}
			}
		}
		return m,nil
	}
	if w.step==aiStepSave {
		switch key {
		case "shift+tab","left","b":
			w.back()
		case "enter","s":
			if w.verified==nil{w.note="Test the provider successfully before saving.";w.step=aiStepTest;return m,nil}
			// Provider setup must not discard existing per-service source mappings.
			w.validatedConfig.ServiceRepos=m.config.AISettings.ServiceRepos
			if err:=ai.SaveSettings(w.validatedConfig);err!=nil{
				w.note="Unable to save your settings: "+err.Error()
				return m,nil
			}
			m.config.AI=w.verified
			m.config.AISettings=w.validatedConfig
			m.config.AIProblem=""
			w.stop()
			m.aiSetup=nil
			m.aiText=""
			m.aiView=false
			m.notice="AI connection validated and settings saved for future launches"
			if m.detail!="" && !m.detailLoading{
				return m.toggleAIExplanation()
			}
		}
		return m,nil
	}
	// A text step, with the key always rendered through a masked textinput.
	switch key {
	case "enter","tab":
		w.advance()
	case "shift+tab":
		w.back()
	default:
		var cmd tea.Cmd
		w.input,cmd=w.input.Update(msg)
		return m,cmd
	}
	return m,nil
}

func (m model) updateAIWizardTest(msg aiSetupTestMsg) (tea.Model,tea.Cmd) {
	w:=m.aiSetup
	if w==nil || msg.generation!=w.generation || w.step!=aiStepTest{return m,nil}
	w.stop()
	if msg.err!=nil{
		w.note="Validation failed: "+msg.err.Error()+". Go back to correct the settings, then retry."
		return m,nil
	}
	w.verified=msg.client
	w.validatedConfig=msg.config
	w.note="Connection verified. A small test request may be billable."
	w.step=aiStepSave
	return m,nil
}

func (m model) renderAIWizard() string {
	w:=m.aiSetup
	if w==nil{return ""}
	width:=max(35,min(m.width-4,86))
	var header strings.Builder
	step,total:=w.step+1,aiStepSave+1
	if w.provider!=aiProviderCompatible{total--;if w.step>aiStepEndpoint{step--}}
	if w.provider!=aiProviderClaude{total--;if w.step>aiStepWorkspace{step--}}
	fmt.Fprintf(&header,"  AI SETUP  ·  Step %d of %d\n\n",step,total)
	labels:=[]string{
		"Select your AI provider",
		"API key",
		"Claude workspace ID (if needed)",
		"Model name",
		"API endpoint URL",
		"Source-code repositories",
		"Input price (USD / million tokens)",
		"Output price (USD / million tokens)",
		"Optional spending allowance (USD / session)",
		"Validate connection",
		"Confirm and save",
	}
	header.WriteString(labels[w.step]+"\n\n")
	switch w.step{
	case aiStepProvider:
		for i,name:=range []string{"OpenAI","Claude (Anthropic API)","Other OpenAI-compatible API"}{
			cursor:="  "
			if w.provider==i{cursor="> "}
			header.WriteString(cursor+name+"\n")
		}
		header.WriteString("\nArrow keys to choose; Enter continues.\nOpenAI and Claude fill their API endpoint automatically.")
	case aiStepKey:
		header.WriteString(w.input.View()+"\n\nYour key stays masked. It will be saved in a user-only file.")
		if w.provider==aiProviderClaude{
			header.WriteString("\n\nCreate an API key at https://platform.claude.com/settings/keys\nEnable API billing/credits in your Claude Console account.\nA Claude chat subscription does not include API usage.\nPaste your API key here; no client ID or secret is needed.")
		}
	case aiStepWorkspace:
		header.WriteString(w.input.View()+"\n\nLeave blank if your key belongs to a single workspace.\nFor a key covering multiple workspaces, paste the workspace ID.\nFind it at https://platform.claude.com/settings/workspaces")
	case aiStepModel:
		header.WriteString(w.input.View()+"\n\nEnter the model ID provided by your API service.")
		if w.provider==aiProviderClaude{
			header.WriteString("\nKeep the suggested Claude model or enter another available ID.\nModels: https://platform.claude.com/docs/en/models/overview\nEndpoint: "+w.fields[aiStepEndpoint]+" (automatic)")
		}
	case aiStepEndpoint:
		header.WriteString(w.input.View()+"\n\nUse the provider's HTTPS base URL ending in /v1.")
	case aiStepRepositories:
		header.WriteString("Choose local folders containing source-code repositories.\n")
		header.WriteString("Arrows navigate, Space selects, A adds a folder.\n\n")
		if w.addRepo{
			header.WriteString("Repository folder path: "+w.input.View()+"\n")
		}else if len(w.repos)==0{
			header.WriteString("No local Git repositories found automatically. Press A to add one.\n")
		}else{
			start:=max(0,min(w.repoIndex-3,len(w.repos)-7))
			end:=min(len(w.repos),start+7)
			for i:=start;i<end;i++{
				cursor:="  "
				if i==w.repoIndex{cursor="> "}
				marker:="[ ]"
				if w.selected[w.repos[i]]{marker="[x]"}
				fmt.Fprintf(&header,"%s%s %s\n",cursor,marker,w.repos[i])
			}
		}
		header.WriteString("\nUnchecked or empty is OK; AI then explains using logs only.")
	case aiStepInputRate,aiStepOutputRate:
		header.WriteString(w.input.View()+"\n\nOptional. Find your actual price in the provider's pricing page.\nLeave blank to hide cost estimates; token usage is still shown.")
	case aiStepBudget:
		header.WriteString(w.input.View()+"\n\nOptional local session allowance. This is NOT an account balance.\nTo enforce it, enter both input and output rates.")
	case aiStepTest:
		header.WriteString("Press Enter to test the provider, model and API key.\n")
		header.WriteString("The test sends only 'Reply OK' — no log or source code.\n")
		header.WriteString("A small test call may incur provider charges.\n")
		if w.busy{header.WriteString("\nTesting connection...")}
	case aiStepSave:
		header.WriteString("Provider and model validated successfully.\n")
		header.WriteString("Press Enter to save settings and use AI immediately.\n\n")
		header.WriteString("Credentials will be stored in a local, owner-restricted\nconfiguration file (not encrypted or in an OS keychain).\n")
		header.WriteString("You can cancel without saving anything.")
	}
	if w.note!=""{header.WriteString("\n\n"+w.note)}
	header.WriteString("\n\nEnter Next/Test/Save   Shift+Tab Back   Esc Cancel")
	content:=lipgloss.NewStyle().Width(width-4).Render(header.String())
	dialog:=lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).
		Background(modalBackground).Foreground(modalForeground).Padding(0,1).Render(content)
	return overlayModal(m.renderAIModalBackground(),dialog,m.width,m.height)
}

func (m model) renderAIModalBackground() string {
	m.aiSetup=nil
	// Avoid recursive rendering by removing the wizard before calling View.
	return m.View()
}
