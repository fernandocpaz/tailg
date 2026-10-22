package tui

import (
    "errors"
    "strings"
    "testing"
    "time"

    "github.com/fernandocpaz/tailg/internal/core"
)

func TestDeploymentCleanEOFReattachesWithoutIssue(t *testing.T) {
    now := time.Now()
    item := core.InventoryItem{Pod: "deployment-api-5d4c", Container: "api"}
    m := model{
        items: []core.InventoryItem{item},
        state: core.NewFilterState(30),
        heartbeat: &core.HeartbeatAnalyzer{},
        followsLive: true, width: 160, height: 20,
        config: Config{Formatter: core.Formatter{Color: false}},
    }
    m.processLogBatch([]core.LogEvent{
        {Pod: item.Pod, Container: item.Container, Message: "INFO Host started", ReceivedAt: now, ObservedAt: now},
        {Pod: item.Pod, Container: item.Container, Closed: true},
    })
    if !m.isReattaching() || m.isReconnecting() {
        t.Fatal("clean EOF must be reattaching, not reconnecting")
    }
    if m.notice != "" || m.issueStats().Groups != 0 {
        t.Fatalf("clean EOF produced an issue: notice %q stats %+v", m.notice, m.issueStats())
    }
    if status := m.freshnessStatus(item, now); status != "REATTACHING" {
        t.Fatalf("stream details status=%q", status)
    }
    header := m.renderHeader()
    if !strings.Contains(header, "REATTACHING LOGS") ||
       strings.Contains(header, "RECONNECTING") || strings.Contains(header, "ISSUE") {
        t.Fatalf("clean EOF must not imply an error: %q", header)
    }
    // Starting another attempt clears the retry indication, but does not
    // assume fresh log lines were received.
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Started: true}})
    if m.isReattaching() || m.isReconnecting() || m.issueStats().Groups != 0 {
        t.Fatal("new stream attempt should clear clean EOF marker")
    }
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Message: "INFO Service running", ReceivedAt: now.Add(time.Second), ObservedAt: now.Add(time.Second)}})
    if m.issueStats().Groups != 0 || m.freshnessStatus(item, now.Add(time.Second)) != "RECEIVING" {
        t.Fatal("normal logs must restore RECEIVING")
    }
}

func TestRealKubectlTransportErrorRemainsAnIssue(t *testing.T) {
    item := core.InventoryItem{Pod: "deployment-api-5d4c", Container: "api"}
    m := model{items: []core.InventoryItem{item}, state: core.NewFilterState(30),
        heartbeat: &core.HeartbeatAnalyzer{}, followsLive: true,
        config: Config{Formatter: core.Formatter{Color: false}},
    }
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Closed: true}})
    if !m.isReattaching() {
        t.Fatal("clean EOF baseline missing")
    }
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Closed: true, Err: errors.New("kubectl connection reset")}})
    if !m.isReconnecting() || m.isReattaching() || m.issueStats().Groups != 1 {
        t.Fatalf("transport failure must be classified: %+v", m.issueStats())
    }
    if !strings.Contains(m.renderHeader(), "RECONNECTING") || !strings.Contains(m.renderHeader(), "1 ISSUE") {
        t.Fatal("real stream failure missing from header")
    }
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Started: true}})
    if m.isReconnecting() {
        t.Fatal("successful retry start must clear reconnect status")
    }
    if m.issueStats().Groups != 1 {
        t.Fatal("real transport errors must remain in issue history")
    }
}

func TestReattachingMarkersPrunedWhenPodRemovedOrCompleted(t *testing.T) {
    item := core.InventoryItem{Pod: "deployment-a", Container: "app"}
    m := model{items: []core.InventoryItem{item}, state: core.NewFilterState(10),
        heartbeat: &core.HeartbeatAnalyzer{}, followsLive: true,
    }
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Closed: true}})
    if !m.isReattaching() {
        t.Fatal("clean EOF should be tracked")
    }
    updated, _ := m.Update(inventoryMsg{items: []core.InventoryItem{{Pod:item.Pod, Container:item.Container, Completed:true}}})
    m = updated.(model)
    if m.isReattaching() || m.isReconnecting() || m.waitingStreams() != 0 {
        t.Fatal("completed pod still shows stream reconnect status")
    }
    m.processLogBatch([]core.LogEvent{{Pod: item.Pod, Container: item.Container, Closed: true}})
    updated, _ = m.Update(inventoryMsg{items: []core.InventoryItem{}})
    m = updated.(model)
    if m.isReattaching() || m.isReconnecting() {
        t.Fatal("deleted pods must not retain reattach markers")
    }
}
