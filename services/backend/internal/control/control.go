// Package control tracks demo start/stop state for pipeline components the UI can toggle.
package control

import "sync"

// Status reports whether each controllable component is currently running.
type Status struct {
	OrchestratorRunning bool `json:"orchestratorRunning"`
	IngestionRunning    bool `json:"ingestionRunning"`
}

// Service holds in-memory run state for components that don't exist as separate
// processes yet. Real process control will replace this once the orchestrator
// and ingestion worker services are built.
type Service struct {
	mu     sync.RWMutex
	status Status
}

// New creates a control Service with everything stopped.
func New() *Service {
	return &Service{}
}

// Status returns the current run state.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// SetOrchestratorRunning updates the orchestrator run state.
func (s *Service) SetOrchestratorRunning(running bool) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.OrchestratorRunning = running
	return s.status
}

// SetIngestionRunning updates the ingestion worker run state.
func (s *Service) SetIngestionRunning(running bool) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.IngestionRunning = running
	return s.status
}
