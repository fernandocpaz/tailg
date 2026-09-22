package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fernandocpaz/tailg/internal/tui"
)

type pickerUsage struct {
	Contexts   map[string]tui.ChoiceUsage            `json:"contexts"`
	Namespaces map[string]map[string]tui.ChoiceUsage `json:"namespaces"`
	path       string
}

func loadPickerUsage() pickerUsage {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return loadPickerUsageAt("")
	}
	return loadPickerUsageAt(filepath.Join(configDir, "tailg", "picker-usage.json"))
}

func loadPickerUsageAt(path string) pickerUsage {
	u := pickerUsage{
		Contexts:   map[string]tui.ChoiceUsage{},
		Namespaces: map[string]map[string]tui.ChoiceUsage{},
		path:       path,
	}
	contents, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(contents, &u)
	}
	if u.Contexts == nil {
		u.Contexts = map[string]tui.ChoiceUsage{}
	}
	if u.Namespaces == nil {
		u.Namespaces = map[string]map[string]tui.ChoiceUsage{}
	}
	return u
}

func (u *pickerUsage) record(kubeContext, namespace string) {
	if kubeContext == "" || namespace == "" {
		return
	}
	now := time.Now().UTC()
	contextUse := u.Contexts[kubeContext]
	contextUse.Count++
	contextUse.LastUsed = now
	u.Contexts[kubeContext] = contextUse
	if u.Namespaces[kubeContext] == nil {
		u.Namespaces[kubeContext] = map[string]tui.ChoiceUsage{}
	}
	namespaceUse := u.Namespaces[kubeContext][namespace]
	namespaceUse.Count++
	namespaceUse.LastUsed = now
	u.Namespaces[kubeContext][namespace] = namespaceUse
	_ = u.save() // A read-only config directory must not block troubleshooting.
}

func (u pickerUsage) save() error {
	if u.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(u.path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(u.path), ".picker-usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := json.NewEncoder(temporary).Encode(u); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), u.path)
}
