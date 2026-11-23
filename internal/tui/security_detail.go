package tui

import "github.com/fernandocpaz/tailg/internal/core"

// showSecurityAssessment evaluates the selected log locally and displays the
// result without replacing the original log or invoking a remote AI provider.
func (m *model) showSecurityAssessment() {
	if m.aiView {
		m.closeAIView()
	}
	record := core.LogRecord{}
	if m.detailRecord != nil {
		record = *m.detailRecord
	} else {
		record.Event.Message = m.detail
		record.Fields = core.ParseLogFields(record.Event)
	}
	m.securityText = core.AssessLogSecurity(record).Text()
	m.securityView = true
	m.detailOffset = 0
}

func (m *model) closeSecurityAssessment() {
	m.securityView = false
	m.securityText = ""
	m.detailOffset = 0
}
