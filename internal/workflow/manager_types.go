package workflow

import (
	"log/slog"

	"spindle/internal/queue"
	"spindle/internal/stage"
)

// StageSet bundles the concrete workflow handlers the manager orchestrates.
type StageSet struct {
	Identifier        stage.Handler
	Ripper            stage.Handler
	AudioAnalysis     stage.Handler
	EpisodeIdentifier stage.Handler
	Encoder           stage.Handler
	Subtitles         stage.Handler
	Organizer         stage.Handler
}

type pipelineStage struct {
	name             string
	handler          stage.Handler
	startStatus      queue.Status
	processingStatus queue.Status
	doneStatus       queue.Status
}

type laneKind string

const (
	laneForeground laneKind = "foreground"
	laneBackground laneKind = "background"
)

type laneState struct {
	kind                 laneKind
	name                 string
	stages               []pipelineStage
	statusOrder          []queue.Status
	stageByStart         map[queue.Status]pipelineStage
	processingStatuses   []queue.Status
	logger               *slog.Logger
	notificationsEnabled bool
	runReclaimer         bool
}

func (l *laneState) finalize() {
	if l == nil {
		return
	}
	l.stageByStart = make(map[queue.Status]pipelineStage, len(l.stages))
	l.statusOrder = make([]queue.Status, 0, len(l.stages))
	seenProcessing := make(map[queue.Status]struct{})
	for _, stg := range l.stages {
		l.stageByStart[stg.startStatus] = stg
		l.statusOrder = append(l.statusOrder, stg.startStatus)
		if stg.processingStatus != "" {
			if _, ok := seenProcessing[stg.processingStatus]; !ok {
				l.processingStatuses = append(l.processingStatuses, stg.processingStatus)
				seenProcessing[stg.processingStatus] = struct{}{}
			}
		}
	}
}

func (l *laneState) stageForStatus(status queue.Status) (pipelineStage, bool) {
	if l == nil {
		return pipelineStage{}, false
	}
	stg, ok := l.stageByStart[status]
	return stg, ok
}
