package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestInspectorObservabilityLayouts(t *testing.T) {
	for _, theme := range []string{"slate", "nightfox"} {
		for _, width := range []int{80, 120} {
			for _, status := range []string{"working", "waiting", "stopped", "offline", "completed"} {
				t.Run(theme+"/"+status+"/"+strconv.Itoa(width), func(t *testing.T) {
					m := newAppTestModel(t)
					m.theme = GetTheme(theme)
					m, _ = updateApp(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
					now := time.Now()
					start := now.Add(-time.Minute).Format(time.RFC3339)
					item := spindle.QueueItem{ID: 4, DisplayTitle: "Long disc title with parallel work", Stage: "encoding", Tasks: []spindle.Task{{ID: 1, Type: "ripping", State: "done"}, {ID: 2, Type: "encoding", State: "running", ActiveAssetKey: "title03", Activities: []spindle.Activity{{ID: "video", Operation: "encoding", State: "running", AssetKey: "title03", Message: "Accepted video frames", StartedAt: start, UpdatedAt: now.Format(time.RFC3339), Total: 1000, Completed: 20, Unit: "frames"}}, Encoding: &spindle.EncodingStatus{ChunksComplete: 1, ChunksTotal: 9, InFlight: 4, Probing: 2, Scoring: 2, TargetWorkers: 4, MaxWorkers: 6, FPS: 40, ETASeconds: 90}}, {ID: 3, Type: "analysis", State: "running", ActiveAssetKey: "title01", Activities: []spindle.Activity{{Operation: "audio", State: "running", AssetKey: "title01", Message: "Analyzing primary and commentary audio", StartedAt: start}}}}, Episodes: []spindle.EpisodeStatus{{Key: "title01", Season: 1, Episode: 1, RippedPath: "rip1.mkv"}, {Key: "title03", Season: 1, Episode: 3, RippedPath: "rip3.mkv", SubtitleSource: "none", SubtitleSkipReason: "No verified English subtitle candidate"}}}
					switch status {
					case "waiting":
						item.Tasks[1].Activities = []spindle.Activity{{State: "waiting", Operation: "input", Message: "Waiting for ripped input", StartedAt: start}}
						item.Tasks[1].ActiveAssetKey = ""
					case "stopped":
						item.UserStopped = true
					case "offline":
						m.snapshot.LastError = errors.New("network unavailable")
						m.snapshot.LastUpdated = now
					case "completed":
						item.Stage = "completed"
						for i := range item.Tasks {
							item.Tasks[i].State = "done"
						}
					}
					m.snapshot.Queue = []spindle.QueueItem{item}
					m.inspectedID = item.ID
					m.inspecting = true
					for tab := tabOverview; tab < tabCount; tab++ {
						m.inspectorTab = tab
						m.updateInspectorViewport()
						m.updateLogViewport()
						view := m.View().Content
						if theme == "nightfox" && width == 80 && status == "working" && (tab == tabOverview || tab == tabEpisodes) {
							t.Log("\n" + stripANSI(view))
						}
						if lipgloss.Height(view) > 24 {
							t.Fatalf("tab %d too tall: %d", tab, lipgloss.Height(view))
						}
						for _, line := range strings.Split(view, "\n") {
							if lipgloss.Width(line) > width {
								t.Fatalf("tab %d overflow: %d > %d: %q", tab, lipgloss.Width(line), width, stripANSI(line))
							}
						}
					}
				})
			}
		}
	}
}

func TestInspectorKeepsReadingAnchorWhenActivitiesExpand(t *testing.T) {
	m := newAppTestModel(t)
	m.width, m.height = 80, 12
	m.inspecting = true
	m.inspectedID = 4
	now := time.Now()
	m.now = func() time.Time { return now }
	item := spindle.QueueItem{ID: 4, Stage: "analysis", Source: &spindle.SourceTitle{TitleID: 3, Name: "Selected feature"}, CreatedAt: now.Format(time.RFC3339), Tasks: []spindle.Task{{Type: "analysis", State: "running", Activities: []spindle.Activity{{Operation: "audio", State: "running", Message: "Analyzing tracks", StartedAt: now.Add(-time.Second).Format(time.RFC3339)}}}}}
	m.snapshot.Queue = []spindle.QueueItem{item}
	m.updateInspectorViewport()
	content := m.renderDetailContent(item, panelInnerWidth(m.width))
	offset := -1
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "Media") {
			offset = i
			break
		}
	}
	if offset < 0 {
		t.Fatal(content)
	}
	m.inspectorViewport.SetYOffset(offset)
	before := strings.TrimSpace(stripANSI(strings.Split(m.inspectorViewport.View(), "\n")[0]))
	now = now.Add(20 * time.Second)
	m.updateInspectorViewport()
	after := strings.TrimSpace(stripANSI(strings.Split(m.inspectorViewport.View(), "\n")[0]))
	if before != after {
		t.Fatalf("reading anchor jumped: %q -> %q", before, after)
	}
}

func TestFetchFailuresStayScopedUntilRecovery(t *testing.T) {
	m := newAppTestModel(t)
	m.width, m.height = 100, 24
	m.inspecting = true
	m.inspectedID = 1
	m.inspectorTab = tabEvents
	m.itemEvents = itemEventState{itemID: 1, loaded: true, events: []spindle.ItemEvent{{ID: 1, Type: "stage_start", Stage: "encoding"}}}
	m, _ = updateApp(t, m, itemEventErrorMsg{itemID: 1, err: errors.New("offline")})
	if got := stripANSI(m.renderItemEvents()); !strings.Contains(got, "Events fetch failed") || !strings.Contains(got, "Worker reserved") {
		t.Fatal(got)
	}
	m.handleItemEventBatch(itemEventBatchMsg{itemID: 2})
	if m.itemEvents.fetchError == nil {
		t.Fatal("other item cleared error")
	}
	m.handleItemEventBatch(itemEventBatchMsg{itemID: 1, batch: spindle.ItemEventBatch{Next: 1}})
	if m.itemEvents.fetchError != nil || len(m.itemEvents.events) != 1 {
		t.Fatal("recovery lost history")
	}
	m.logState.mode = logSourceItem
	m.logState.lastItemID = 1
	m.logState.generation = 2
	m, _ = updateApp(t, m, logErrorMsg{source: logSourceItem, itemID: 1, generation: 1, err: errors.New("old filters")})
	if m.logState.fetchError != nil {
		t.Fatal("old filter request poisoned current view")
	}
	m, _ = updateApp(t, m, logErrorMsg{source: logSourceItem, itemID: 1, generation: 2, err: errors.New("offline")})
	if m.logState.fetchError == nil {
		t.Fatal("fetch error disappeared")
	}
	m.handleLogBatch(logBatchMsg{source: logSourceItem, itemID: 1, generation: 2})
	if m.logState.fetchError != nil || !m.logState.loaded {
		t.Fatal("successful empty fetch not distinguished from failure")
	}
}
