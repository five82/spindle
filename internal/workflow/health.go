package workflow

// StageHealth summarizes the readiness of a workflow stage.
type StageHealth struct {
	Name   string
	Ready  bool
	Detail string
}

// HealthyStage constructs a ready StageHealth record.
func HealthyStage(name string) StageHealth {
	return StageHealth{Name: name, Ready: true}
}

// UnhealthyStage constructs an unhealthy StageHealth record with context detail.
func UnhealthyStage(name, detail string) StageHealth {
	return StageHealth{Name: name, Ready: false, Detail: detail}
}
