package workflow

import "spindle/internal/queue"

// ConfigureStages registers the concrete stage handlers the workflow will run.
func (m *Manager) ConfigureStages(set StageSet) {
	foreground := &laneState{kind: laneForeground, name: "foreground", notificationsEnabled: true}
	background := &laneState{kind: laneBackground, name: "background", notificationsEnabled: false}

	if set.Identifier != nil {
		foreground.stages = append(foreground.stages, pipelineStage{
			name:             "identifier",
			handler:          set.Identifier,
			startStatus:      queue.StatusPending,
			processingStatus: queue.StatusIdentifying,
			doneStatus:       queue.StatusIdentified,
		})
	}
	if set.Ripper != nil {
		foreground.stages = append(foreground.stages, pipelineStage{
			name:             "ripper",
			handler:          set.Ripper,
			startStatus:      queue.StatusIdentified,
			processingStatus: queue.StatusRipping,
			doneStatus:       queue.StatusRipped,
		})
	}
	// Audio analysis runs in background lane after ripping
	audioAnalyzedStatus := queue.StatusRipped
	if set.AudioAnalysis != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "audio-analysis",
			handler:          set.AudioAnalysis,
			startStatus:      queue.StatusRipped,
			processingStatus: queue.StatusAudioAnalyzing,
			doneStatus:       queue.StatusAudioAnalyzed,
		})
		audioAnalyzedStatus = queue.StatusAudioAnalyzed
	}
	encoderStart := audioAnalyzedStatus
	if set.EpisodeIdentifier != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "episode-identifier",
			handler:          set.EpisodeIdentifier,
			startStatus:      audioAnalyzedStatus,
			processingStatus: queue.StatusEpisodeIdentifying,
			doneStatus:       queue.StatusEpisodeIdentified,
		})
		encoderStart = queue.StatusEpisodeIdentified
	}
	organizerStart := queue.StatusEncoded
	if set.Encoder != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "encoder",
			handler:          set.Encoder,
			startStatus:      encoderStart,
			processingStatus: queue.StatusEncoding,
			doneStatus:       queue.StatusEncoded,
		})
	}
	if set.Subtitles != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "subtitles",
			handler:          set.Subtitles,
			startStatus:      queue.StatusEncoded,
			processingStatus: queue.StatusSubtitling,
			doneStatus:       queue.StatusSubtitled,
		})
		organizerStart = queue.StatusSubtitled
	}
	if set.Organizer != nil {
		background.stages = append(background.stages, pipelineStage{
			name:             "organizer",
			handler:          set.Organizer,
			startStatus:      organizerStart,
			processingStatus: queue.StatusOrganizing,
			doneStatus:       queue.StatusCompleted,
		})
	}

	lanes := make(map[laneKind]*laneState)
	order := make([]laneKind, 0, 2)

	if len(foreground.stages) > 0 {
		foreground.finalize()
		lanes[foreground.kind] = foreground
		order = append(order, foreground.kind)
	}
	if len(background.stages) > 0 {
		background.finalize()
		lanes[background.kind] = background
		order = append(order, background.kind)
	}

	for _, lane := range lanes {
		if lane == nil {
			continue
		}
		lane.runReclaimer = len(lane.processingStatuses) > 0
	}

	m.mu.Lock()
	m.lanes = lanes
	m.laneOrder = order
	m.mu.Unlock()
}
