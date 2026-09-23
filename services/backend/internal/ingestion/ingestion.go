// Package ingestion will own ingestion run control and watermark reporting.
package ingestion

// Status is a placeholder for per-source-table ingestion state.
type Status struct {
	Source     string `json:"source"`
	Table      string `json:"table"`
	Watermark  string `json:"watermark"`
	RowsLoaded int64  `json:"rowsLoaded"`
}

// Service will coordinate ingestion runs once the ingestion worker exists.
type Service struct{}

// New creates an ingestion Service.
func New() *Service {
	return &Service{}
}

// Statuses returns current ingestion state, currently empty until the ingestion worker is wired in.
func (s *Service) Statuses() []Status {
	return []Status{}
}
