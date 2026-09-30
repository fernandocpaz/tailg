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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	azureDevOpsAPIVersion          = "7.1"
	defaultSimpleUploadLimit int64 = 128 * 1024 * 1024
	defaultUploadChunkSize   int64 = 4 * 1024 * 1024
)

var (
	incidentKeyPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	workItemTypePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _-]{0,63}$`)
	workItemTagPattern  = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	attachmentIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)
)

// AzureDevOpsWorkItems creates/reuses work items using the Azure DevOps REST
// API. BaseURL is injectable for tests; production should leave it empty.
type AzureDevOpsWorkItems struct {
	Organization      string
	Project           string
	WorkItemType      string
	Token             string
	BaseURL           string
	Client            *http.Client
	simpleUploadLimit int64 // injectable for tests
	uploadChunkSize   int64 // injectable for tests
}

type WorkItemResult struct {
	Provider      string `json:"provider"`
	ID            int    `json:"id"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	Existing      bool   `json:"existing"`
	EvidenceID    string `json:"evidenceId,omitempty"`
	AttachmentURL string `json:"attachmentUrl,omitempty"`
	Attached      bool   `json:"attached"`
}

type workItemPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

type azureWorkItem struct {
	ID     int `json:"id"`
	Rev    int `json:"rev"`
	Fields struct {
		Title string `json:"System.Title"`
	} `json:"fields"`
	Relations []struct {
		Rel        string `json:"rel"`
		URL        string `json:"url"`
		Attributes struct {
			Comment string `json:"comment"`
		} `json:"attributes"`
	} `json:"relations"`
}

type azureAttachment struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (a AzureDevOpsWorkItems) CreateForIncident(ctx context.Context, incident Incident, title, description string) (WorkItemResult, error) {
	return a.CreateForIncidentWithEvidence(ctx, incident, title, description, "", "", MonitorSpec{})
}

// CreateForIncidentWithEvidence attaches the complete saved evidence text. A
// retry reuses the incident work item and an existing evidence relation.
func (a AzureDevOpsWorkItems) CreateForIncidentWithEvidence(ctx context.Context, incident Incident, title, description, evidenceDir, evidenceID string, spec MonitorSpec) (WorkItemResult, error) {
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
	var evidenceFile *os.File
	var manifest EvidenceManifest
	if evidenceID != "" {
		var evidenceErr error
		evidenceFile, manifest, evidenceErr = openWorkItemEvidence(evidenceDir, evidenceID, incident, spec)
		if evidenceErr != nil {
			return WorkItemResult{}, evidenceErr
		}
		defer evidenceFile.Close()
	}
	base, err := a.apiBase()
	if err != nil {
		return WorkItemResult{}, err
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
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
	var result WorkItemResult
	if len(matches.WorkItems) > 0 {
		id := matches.WorkItems[0].ID
		result = WorkItemResult{Provider: "azure-devops", ID: id, URL: a.workItemURL(id), Existing: true}
	} else {
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
			ID     int `json:"id"`
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
		result = WorkItemResult{Provider: "azure-devops", ID: created.ID, URL: a.workItemURL(created.ID), Title: title}
	}
	if evidenceFile == nil {
		if result.Existing {
			var existing azureWorkItem
			readURL := fmt.Sprintf("%s/_apis/wit/workitems/%d?fields=System.Title&api-version=%s", base, result.ID, azureDevOpsAPIVersion)
			_ = a.doJSON(ctx, client, http.MethodGet, readURL, "application/json", nil, &existing)
			result.Title = existing.Fields.Title
		}
		return result, nil
	}
	result.EvidenceID = evidenceID
	if err := a.attachEvidence(ctx, client, base, result.ID, evidenceFile, manifest, &result); err != nil {
		return WorkItemResult{}, fmt.Errorf("work item #%d exists, but evidence attachment failed; retry the same incidentId: %w", result.ID, err)
	}
	return result, nil
}

func openWorkItemEvidence(dir, id string, incident Incident, spec MonitorSpec) (*os.File, EvidenceManifest, error) {
	if !validHash(id) || dir == "" {
		return nil, EvidenceManifest{}, errors.New("a valid evidenceId and configured --evidence-dir are required")
	}
	if err := validateExistingDirectory(dir); err != nil {
		return nil, EvidenceManifest{}, err
	}
	path := filepath.Join(dir, id)
	manifest, err := readEvidenceManifest(path, id)
	if err != nil {
		return nil, EvidenceManifest{}, err
	}
	if manifest.IssueID != incident.IssueID || manifest.Scope.Context != spec.Context || manifest.Scope.Namespace != spec.Namespace {
		return nil, EvidenceManifest{}, errors.New("evidence does not match the incident issue ID and pinned monitor scope")
	}
	contentPath := filepath.Join(path, "content.txt")
	info, err := os.Lstat(contentPath)
	if err != nil {
		return nil, EvidenceManifest{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || int64(manifest.TotalBytes) != info.Size() {
		return nil, EvidenceManifest{}, errors.New("evidence content file permissions, type, or size are invalid")
	}
	// Unlike paginated reads, an external upload always verifies the full file.
	if err := verifyEvidenceContent(contentPath, manifest, id); err != nil {
		return nil, EvidenceManifest{}, err
	}
	f, err := os.Open(contentPath)
	if err != nil {
		return nil, EvidenceManifest{}, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || opened.Size() != info.Size() {
		f.Close()
		return nil, EvidenceManifest{}, errors.New("evidence content changed while opening")
	}
	return f, manifest, nil
}

func (a AzureDevOpsWorkItems) attachEvidence(ctx context.Context, client *http.Client, base string, workItemID int, f *os.File, manifest EvidenceManifest, result *WorkItemResult) error {
	comment := "Tailg evidence " + manifest.EvidenceID
	readURL := fmt.Sprintf("%s/_apis/wit/workitems/%d?$expand=relations&api-version=%s", base, workItemID, azureDevOpsAPIVersion)
	var existing azureWorkItem
	if err := a.doJSON(ctx, client, http.MethodGet, readURL, "application/json", nil, &existing); err != nil {
		return fmt.Errorf("could not inspect existing work item attachments: %w", err)
	}
	if existing.ID != workItemID {
		return errors.New("Azure DevOps returned a different work item ID")
	}
	if result.Existing {
		result.Title = existing.Fields.Title
	}
	if attachmentURL := evidenceRelationURL(existing, comment); attachmentURL != "" {
		result.Attached, result.AttachmentURL = true, attachmentURL
		return nil
	}
	if existing.Rev <= 0 {
		return errors.New("Azure DevOps did not return a valid work item revision")
	}
	attachment, err := a.uploadEvidence(ctx, client, base, f, manifest)
	if err != nil {
		return err
	}
	if err := verifyEvidenceContent(f.Name(), manifest, manifest.EvidenceID); err != nil {
		return fmt.Errorf("evidence changed during upload: %w", err)
	}
	patch := []workItemPatch{
		{Op: "test", Path: "/rev", Value: existing.Rev},
		{Op: "add", Path: "/relations/-", Value: map[string]any{
			"rel": "AttachedFile", "url": attachment.URL, "attributes": map[string]string{"comment": comment},
		}},
	}
	body, _ := json.Marshal(patch)
	updateURL := fmt.Sprintf("%s/_apis/wit/workitems/%d?api-version=%s", base, workItemID, azureDevOpsAPIVersion)
	var updated azureWorkItem
	if err := a.doJSON(ctx, client, http.MethodPatch, updateURL, "application/json-patch+json", body, &updated); err != nil {
		// A competing monitor may have attached the same snapshot while we
		// uploaded it. The revision test prevents two relations from winning.
		var current azureWorkItem
		if readErr := a.doJSON(ctx, client, http.MethodGet, readURL, "application/json", nil, &current); readErr == nil && current.ID == workItemID {
			if attachmentURL := evidenceRelationURL(current, comment); attachmentURL != "" {
				result.Attached, result.AttachmentURL = true, attachmentURL
				return nil
			}
		}
		return fmt.Errorf("could not link the uploaded evidence to the work item: %w", err)
	}
	if updated.ID != workItemID {
		return errors.New("Azure DevOps did not confirm the evidence link on the requested work item")
	}
	result.Attached, result.AttachmentURL = true, attachment.URL
	return nil
}

func evidenceRelationURL(item azureWorkItem, comment string) string {
	for _, relation := range item.Relations {
		if relation.Rel == "AttachedFile" && relation.Attributes.Comment == comment {
			return relation.URL
		}
	}
	return ""
}

func (a AzureDevOpsWorkItems) uploadEvidence(ctx context.Context, client *http.Client, base string, f *os.File, manifest EvidenceManifest) (azureAttachment, error) {
	fileName := "tailg-evidence-" + manifest.EvidenceID + ".txt"
	baseURL := base + "/_apis/wit/attachments?fileName=" + url.QueryEscape(fileName) + "&api-version=" + azureDevOpsAPIVersion
	limit := a.simpleUploadLimit
	if limit <= 0 {
		limit = defaultSimpleUploadLimit
	}
	var attachment azureAttachment
	if int64(manifest.TotalBytes) <= limit {
		if err := a.doStreamJSON(ctx, client, http.MethodPost, baseURL, "application/octet-stream", f, int64(manifest.TotalBytes), "", &attachment); err != nil {
			return azureAttachment{}, fmt.Errorf("Azure DevOps evidence upload failed: %w", err)
		}
	} else {
		startURL := baseURL + "&uploadType=chunked"
		if err := a.doJSON(ctx, client, http.MethodPost, startURL, "application/octet-stream", nil, &attachment); err != nil {
			return azureAttachment{}, fmt.Errorf("Azure DevOps chunked upload could not start: %w", err)
		}
		if !validAttachmentID(attachment.ID) {
			return azureAttachment{}, errors.New("Azure DevOps returned an invalid attachment ID")
		}
		chunkSize := a.uploadChunkSize
		if chunkSize <= 0 {
			chunkSize = defaultUploadChunkSize
		}
		chunkURL := base + "/_apis/wit/attachments/" + attachment.ID + "?api-version=" + azureDevOpsAPIVersion
		for offset := int64(0); offset < int64(manifest.TotalBytes); offset += chunkSize {
			size := min(chunkSize, int64(manifest.TotalBytes)-offset)
			contentRange := fmt.Sprintf("bytes %d-%d/%d", offset, offset+size-1, manifest.TotalBytes)
			if err := a.doStreamJSON(ctx, client, http.MethodPut, chunkURL, "application/octet-stream", io.NewSectionReader(f, offset, size), size, contentRange, nil); err != nil {
				return azureAttachment{}, fmt.Errorf("Azure DevOps evidence chunk at offset %d failed: %w", offset, err)
			}
		}
	}
	if !validAttachmentID(attachment.ID) || !sameAttachmentHost(base, attachment.URL) {
		return azureAttachment{}, errors.New("Azure DevOps returned an invalid attachment reference")
	}
	return attachment, nil
}

func validAttachmentID(id string) bool {
	return attachmentIDPattern.MatchString(id)
}

func sameAttachmentHost(base, target string) bool {
	baseURL, baseErr := url.Parse(base)
	targetURL, targetErr := url.Parse(target)
	return baseErr == nil && targetErr == nil && baseURL.Scheme == targetURL.Scheme && baseURL.Host == targetURL.Host && strings.Contains(targetURL.Path, "/_apis/wit/attachments/")
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
	return a.doStreamJSON(ctx, client, method, target, contentType, bytes.NewReader(body), int64(len(body)), "", output)
}

func (a AzureDevOpsWorkItems) doStreamJSON(ctx context.Context, client *http.Client, method, target, contentType string, body io.Reader, bodySize int64, contentRange string, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.ContentLength = bodySize
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+a.Token)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
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
