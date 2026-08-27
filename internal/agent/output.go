package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	envelope := ErrorEnvelope{SchemaVersion: SchemaVersion, Kind: "CollectionError"}
	envelope.Error.Code = code
	envelope.Error.Message = Redact(message)
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
		case len(report.Pods) > 0:
			report.Pods = report.Pods[:len(report.Pods)-1]
		case len(report.Issues) > 0:
			report.Issues = report.Issues[:len(report.Issues)-1]
			refreshSummary(&report)
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
		return nil, fmt.Errorf("unsupported output format %q (use json or ndjson)", format)
	}
	write := func(kind string, value any) error {
		return encoder.Encode(map[string]any{"schemaVersion": SchemaVersion, "type": kind, "data": value})
	}
	header := struct {
		Kind        string  `json:"kind"`
		GeneratedAt string  `json:"generatedAt"`
		Window      string  `json:"window"`
		Scope       Scope   `json:"scope"`
		Limits      Limits  `json:"limits"`
		Summary     Summary `json:"summary"`
		Truncated   bool    `json:"truncated"`
	}{report.Kind, report.GeneratedAt, report.Window, report.Scope, report.Limits, report.Summary, report.Truncated}
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
