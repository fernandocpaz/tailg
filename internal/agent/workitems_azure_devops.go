package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const azureDevOpsAPIVersion = "7.1"

var (
	incidentKeyPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	workItemTypePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _-]{0,63}$`)
	workItemTagPattern  = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

// AzureDevOpsWorkItems creates/reuses work items using the Azure DevOps REST
// API. BaseURL is injectable for tests; production should leave it empty.
type AzureDevOpsWorkItems struct {
	Organization string
	Project      string
	WorkItemType string
	Token        string
	BaseURL      string
	Client       *http.Client
}

type WorkItemResult struct {
	Provider string `json:"provider"`
	ID       int    `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Existing bool   `json:"existing"`
}

type workItemPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func (a AzureDevOpsWorkItems) CreateForIncident(ctx context.Context, incident Incident, title, description string) (WorkItemResult, error) {
	if !incidentKeyPattern.MatchString(incident.ID) {
		return WorkItemResult{}, errors.New("incident ID is invalid")
	}
	if incident.Status != "open" {
		return WorkItemResult{}, errors.New("work items can only be created for open incidents")
	}
	a.Organization = strings.TrimSpace(a.Organization)
	a.Project = strings.TrimSpace(a.Project)
	a.Token = strings.TrimSpace(a.Token)
	if a.Organization == "" || a.Project == "" || a.Token == "" {
		return WorkItemResult{}, errors.New("Azure DevOps requires organization, project, and TAILG_AZDO_TOKEN")
	}
	if a.WorkItemType == "" {
		a.WorkItemType = "Bug"
	}
	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("[Tailg] %s: %s", strings.ToUpper(incident.Severity), incident.Summary)
	}
	title = strings.TrimSpace(Redact(title))
	if title == "" || len(title) > 200 {
		return WorkItemResult{}, errors.New("work item title must contain 1 to 200 characters")
	}
	if len(description) > 20000 {
		return WorkItemResult{}, errors.New("work item description exceeds 20000 characters")
	}
	if !workItemTypePattern.MatchString(a.WorkItemType) {
		return WorkItemResult{}, errors.New("Azure DevOps work item type contains unsupported characters")
	}
	base, err := a.apiBase()
	if err != nil {
		return WorkItemResult{}, err
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	tag := "TailgIncident-" + incident.ID

	query := fmt.Sprintf("SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project AND [System.Tags] CONTAINS '%s'", tag)
	queryBody, _ := json.Marshal(map[string]string{"query": query})
	queryURL := base + "/_apis/wit/wiql?api-version=" + azureDevOpsAPIVersion
	var matches struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	if err := a.doJSON(ctx, client, http.MethodPost, queryURL, "application/json", queryBody, &matches); err != nil {
		return WorkItemResult{}, fmt.Errorf("Azure DevOps incident lookup failed: %w", err)
	}
	if len(matches.WorkItems) > 0 {
		id := matches.WorkItems[0].ID
		var existing struct {
			Fields struct {
				Title string `json:"System.Title"`
			} `json:"fields"`
		}
		readURL := fmt.Sprintf("%s/_apis/wit/workitems/%d?fields=System.Title&api-version=%s", base, id, azureDevOpsAPIVersion)
		_ = a.doJSON(ctx, client, http.MethodGet, readURL, "application/json", nil, &existing)
		return WorkItemResult{Provider: "azure-devops", ID: id, URL: a.workItemURL(id), Title: existing.Fields.Title, Existing: true}, nil
	}

	detail := incidentDescription(incident, Redact(description))
	tags := tag
	if strings.TrimSpace(incident.Service) != "" {
		tags += "; " + normalizeWorkItemTag(incident.Service)
	}
	patch := []workItemPatch{
		{Op: "add", Path: "/fields/System.Title", Value: title},
		{Op: "add", Path: "/fields/System.Description", Value: detail},
		{Op: "add", Path: "/fields/System.Tags", Value: tags},
	}
	body, _ := json.Marshal(patch)
	workItemURL := base + "/_apis/wit/workitems/$" + url.PathEscape(a.WorkItemType) + "?api-version=" + azureDevOpsAPIVersion
	var created struct {
		ID     int    `json:"id"`
		URL    string `json:"url"`
		Fields struct {
			Title string `json:"System.Title"`
		} `json:"fields"`
	}
	if err := a.doJSON(ctx, client, http.MethodPost, workItemURL, "application/json-patch+json", body, &created); err != nil {
		return WorkItemResult{}, fmt.Errorf("Azure DevOps work item creation failed: %w", err)
	}
	if created.ID <= 0 {
		return WorkItemResult{}, errors.New("Azure DevOps returned a work item without an ID")
	}
	if created.Fields.Title != "" {
		title = created.Fields.Title
	}
	return WorkItemResult{Provider: "azure-devops", ID: created.ID, URL: a.workItemURL(created.ID), Title: title}, nil
}

func (a AzureDevOpsWorkItems) apiBase() (string, error) {
	if strings.ContainsAny(a.Organization, "/\\?#") || strings.ContainsAny(a.Project, "/\\?#") {
		return "", errors.New("Azure DevOps organization or project contains unsupported URL characters")
	}
	if a.BaseURL != "" {
		parsed, err := url.Parse(a.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", errors.New("Azure DevOps base URL is invalid")
		}
		return strings.TrimRight(a.BaseURL, "/") + "/" + url.PathEscape(a.Organization) + "/" + url.PathEscape(a.Project), nil
	}
	return "https://dev.azure.com/" + url.PathEscape(a.Organization) + "/" + url.PathEscape(a.Project), nil
}

func (a AzureDevOpsWorkItems) workItemURL(id int) string {
	return "https://dev.azure.com/" + url.PathEscape(a.Organization) + "/" + url.PathEscape(a.Project) + "/_workitems/edit/" + fmt.Sprint(id)
}

func (a AzureDevOpsWorkItems) doJSON(ctx context.Context, client *http.Client, method, target, contentType string, body []byte, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+a.Token)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(output); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	return nil
}

func incidentDescription(incident Incident, agentDescription string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<p>Created by Tailg for Kubernetes incident <code>%s</code>.</p><ul>", html.EscapeString(incident.ID))
	for _, field := range []struct{ name, value string }{
		{"Severity", incident.Severity}, {"Status", incident.Status}, {"Workload", incident.Workload},
		{"Kind", incident.Kind}, {"Service", incident.Service}, {"First seen", incident.FirstSeen},
		{"Last seen", incident.LastSeen}, {"Affected pods", strings.Join(incident.Pods, ", ")},
		{"Occurrences", fmt.Sprint(incident.Occurrence)}, {"Events in last window", fmt.Sprint(incident.WindowCount)},
	} {
		if strings.TrimSpace(field.value) != "" {
			fmt.Fprintf(&b, "<li><strong>%s:</strong> %s</li>", html.EscapeString(field.name), html.EscapeString(field.value))
		}
	}
	b.WriteString("</ul><p><strong>Tailg summary:</strong></p><pre>")
	b.WriteString(html.EscapeString(incident.Summary))
	b.WriteString("</pre>")
	if strings.TrimSpace(agentDescription) != "" {
		b.WriteString("<p><strong>Agent notes and evidence:</strong></p><pre>")
		b.WriteString(html.EscapeString(agentDescription))
		b.WriteString("</pre>")
	}
	return b.String()
}

func normalizeWorkItemTag(value string) string {
	value = strings.TrimSpace(workItemTagPattern.ReplaceAllString(value, "-"))
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}
