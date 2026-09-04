package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func WriteReport(output io.Writer, report Report, format string) error {
	limited, err := fitReport(report, format)
	if err != nil {
		return err
	}
	_, err = output.Write(limited)
	return err
}

func WriteError(output io.Writer, code, message, format string) error {
	message = Redact(message)
	if format == "text" {
		_, err := fmt.Fprintf(output, "ERROR | %s | %s\n", code, message)
		return err
	}
	envelope := ErrorEnvelope{SchemaVersion: SchemaVersion, Kind: "CollectionError"}
	envelope.Error.Code = code
	envelope.Error.Message = message
	if format == "ndjson" {
		return json.NewEncoder(output).Encode(map[string]any{"schemaVersion": SchemaVersion, "type": "error", "data": envelope.Error})
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

func fitReport(report Report, format string) ([]byte, error) {
	report, err := LimitReport(report, format)
	if err != nil {
		return nil, err
	}
	return encodeReport(report, format)
}

func LimitReport(report Report, format string) (Report, error) {
	for {
		data, err := encodeReport(report, format)
		if err != nil {
			return Report{}, err
		}
		if report.Limits.MaxBytes <= 0 || len(data) <= report.Limits.MaxBytes {
			return report, nil
		}
		report.Truncated = true
		switch {
		case len(report.KubernetesEvents) > 0:
			report.KubernetesEvents = report.KubernetesEvents[:len(report.KubernetesEvents)-1]
		case trimOneContext(&report):
		case len(report.Recommendations) > 1:
			report.Recommendations = report.Recommendations[:len(report.Recommendations)-1]
		case len(report.Pods) > 0:
			report.Pods = report.Pods[:len(report.Pods)-1]
		case len(report.Issues) > 0:
			report.Issues = report.Issues[:len(report.Issues)-1]
			refreshSummary(&report)
		case len(report.Recommendations) > 0:
			report.Recommendations = nil
		case len(report.CollectionErrors) > 0:
			report.CollectionErrors = report.CollectionErrors[:len(report.CollectionErrors)-1]
		default:
			return Report{}, fmt.Errorf("--max-bytes is too small for the report envelope")
		}
	}
}

func trimOneContext(report *Report) bool {
	for index := len(report.Issues) - 1; index >= 0; index-- {
		context := &report.Issues[index].Context
		switch {
		case len(context.After) > 0:
			context.After = context.After[:len(context.After)-1]
			return true
		case len(context.Before) > 0:
			context.Before = context.Before[1:]
			return true
		case context.Match.Message != "":
			context.Match.Message = ""
			return true
		}
	}
	return false
}

func encodeReport(report Report, format string) ([]byte, error) {
	if format == "text" {
		return encodeTextReport(report), nil
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if format == "json" {
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return nil, err
		}
		return output.Bytes(), nil
	}
	if format != "ndjson" {
		return nil, fmt.Errorf("unsupported output format %q (use json, ndjson, or text)", format)
	}
	write := func(kind string, value any) error {
		return encoder.Encode(map[string]any{"schemaVersion": SchemaVersion, "type": kind, "data": value})
	}
	header := struct {
		Kind            string   `json:"kind"`
		GeneratedAt     string   `json:"generatedAt"`
		Window          string   `json:"window"`
		Scope           Scope    `json:"scope"`
		Limits          Limits   `json:"limits"`
		Summary         Summary  `json:"summary"`
		Recommendations []string `json:"recommendations,omitempty"`
		Truncated       bool     `json:"truncated"`
	}{report.Kind, report.GeneratedAt, report.Window, report.Scope, report.Limits, report.Summary, report.Recommendations, report.Truncated}
	if err := write("summary", header); err != nil {
		return nil, err
	}
	for _, pod := range report.Pods {
		if err := write("pod", pod); err != nil {
			return nil, err
		}
	}
	for _, event := range report.KubernetesEvents {
		if err := write("kubernetesEvent", event); err != nil {
			return nil, err
		}
	}
	for _, issue := range report.Issues {
		if err := write("issue", issue); err != nil {
			return nil, err
		}
	}
	for _, item := range report.CollectionErrors {
		if err := write("collectionError", item); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}

func encodeTextReport(report Report) []byte {
	var output strings.Builder
	status := strings.ToUpper(valueOr(report.Summary.Status, "unknown"))
	fmt.Fprintf(&output, "%s | %s | namespace=%s | target=%s | pods=%d | issues=%d | events=%d\n",
		status, report.Kind, valueOr(report.Scope.Namespace, "default"), valueOr(report.Scope.Target, "pod/*"), len(report.Scope.Pods), report.Summary.IssueGroups, report.Summary.IssueEvents)
	if report.GeneratedAt != "" {
		fmt.Fprintf(&output, "generated=%s | window=%s | log-lines=%d", report.GeneratedAt, report.Window, report.Summary.LogLines)
		if report.Truncated {
			output.WriteString(" | TRUNCATED")
		}
		output.WriteString("\n")
	}

	if len(report.Pods) > 0 {
		output.WriteString("\nPODS\n")
		for _, pod := range report.Pods {
			marker := "OK"
			if len(pod.Issues) > 0 {
				marker = "!!"
			}
			fmt.Fprintf(&output, "%s %s | phase=%s | ready=%d/%d | restarts=%d\n", marker, pod.Name, pod.Phase, pod.Ready, pod.Total, pod.Restarts)
			for _, container := range pod.Containers {
				parts := []string{valueOr(container.Kind, "container") + "=" + container.Name, "state=" + valueOr(container.State, "unknown"), fmt.Sprintf("ready=%t", container.Ready)}
				if container.Reason != "" {
					parts = append(parts, "reason="+container.Reason)
				}
				if container.Restarts > 0 {
					parts = append(parts, fmt.Sprintf("restarts=%d", container.Restarts))
				}
				if container.StartedAt != "" {
					parts = append(parts, "started="+container.StartedAt)
				}
				if container.LastReason != "" {
					last := "last=" + container.LastReason
					if container.LastExitCode != 0 {
						last += fmt.Sprintf("/exit=%d", container.LastExitCode)
					}
					if container.LastFinishedAt != "" {
						last += "/at=" + container.LastFinishedAt
					}
					parts = append(parts, last)
				}
				fmt.Fprintf(&output, "   - %s\n", strings.Join(parts, " | "))
			}
			for _, issue := range pod.Issues {
				fmt.Fprintf(&output, "   ! %s\n", issue)
			}
		}
	}

	if len(report.KubernetesEvents) > 0 {
		output.WriteString("\nKUBERNETES WARNINGS\n")
		for _, event := range report.KubernetesEvents {
			prefix := strings.TrimSpace(strings.Join([]string{event.Timestamp, event.Reason, event.Object}, " | "))
			fmt.Fprintf(&output, "- %s | %s\n", prefix, event.Message)
		}
	}

	if len(report.Issues) > 0 {
		output.WriteString("\nLOG ISSUES\n")
		for _, issue := range report.Issues {
			trend := ""
			if issue.Increasing {
				trend = " | increasing"
			}
			fmt.Fprintf(&output, "%s %s x%d | service=%s | id=%s%s\n", strings.ToUpper(issue.Severity), issue.Kind, issue.Count, issue.Service, issue.ID, trend)
			if len(issue.Pods) > 0 {
				fmt.Fprintf(&output, "   pods: %s\n", strings.Join(issue.Pods, ", "))
			}
			fmt.Fprintf(&output, "   %s\n", issue.Summary)
			for _, line := range issue.Context.Before {
				writeTextLogLine(&output, "   ", line)
			}
			if issue.Context.Match.Message != "" {
				writeTextLogLine(&output, " > ", issue.Context.Match)
			}
			for _, line := range issue.Context.After {
				writeTextLogLine(&output, "   ", line)
			}
		}
	}

	if len(report.Recommendations) > 0 {
		output.WriteString("\nNEXT ACTIONS\n")
		for index, recommendation := range report.Recommendations {
			fmt.Fprintf(&output, "%d. %s\n", index+1, recommendation)
		}
	}

	if len(report.CollectionErrors) > 0 {
		output.WriteString("\nCOLLECTION WARNINGS\n")
		for _, item := range report.CollectionErrors {
			fmt.Fprintf(&output, "- %s: %s\n", item.Source, item.Message)
		}
	}
	return []byte(output.String())
}

func writeTextLogLine(output *strings.Builder, marker string, line LogLine) {
	parts := make([]string, 0, 3)
	if line.Timestamp != "" {
		parts = append(parts, line.Timestamp)
	}
	if line.Pod != "" || line.Container != "" {
		parts = append(parts, strings.Trim(line.Pod+"/"+line.Container, "/"))
	}
	parts = append(parts, line.Message)
	fmt.Fprintf(output, "%s%s\n", marker, strings.Join(parts, " | "))
}
