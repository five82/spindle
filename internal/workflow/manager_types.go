package workflow

import (
	"context"
	"log/slog"

	"spindle/internal/queue"
	"spindle/internal/stage"
)

// StageHandler describes the narrow contract the manager needs from each stage.
type StageHandler interface {
	Prepare(context.Context, *queue.Item) error
	Execute(context.Context, *queue.Item) error
	HealthCheck(context.Context) stage.Health
}

// StageSet bundles the concrete workflow handlers the manager orchestrates.
type StageSet struct {
	Identifier        StageHandler
	Ripper            StageHandler
	EpisodeIdentifier StageHandler
	Encoder           StageHandler
	Subtitles         StageHandler
	Organizer         StageHandler
}

type pipelineStage struct {
	name             string
	handler          StageHandler
	startStatus      queue.Status
	processingStatus queue.Status
	doneStatus       queue.Status
}

type loggerAware interface {
	SetLogger(*slog.Logger)
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
