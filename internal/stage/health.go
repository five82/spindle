package stage

// Health summarizes the readiness of a workflow stage.
type Health struct {
	Name   string
	Ready  bool
	Detail string
}

// Healthy constructs a ready Health record.
func Healthy(name string) Health {
	return Health{Name: name, Ready: true}
}

// Unhealthy constructs an unhealthy Health record with context detail.
func Unhealthy(name, detail string) Health {
	return Health{Name: name, Ready: false, Detail: detail}
}
