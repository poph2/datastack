// Package control exposes start/stop for the pipeline components the UI can toggle.
package control

import (
	"sync"

	"github.com/poph2/datastack/services/backend/internal/orchestrator"
)

// Status reports whether each controllable component is currently running.
type Status struct {
	OrchestratorRunning  bool `json:"orchestratorRunning"`
	OrchestratorCaughtUp bool `json:"orchestratorCaughtUp"`
	IngestionRunning     bool `json:"ingestionRunning"`
}

// Service starts/stops the orchestrator; ingestion is still an in-memory flag until the worker exists.
type Service struct {
	orchestrator *orchestrator.Orchestrator

	mu               sync.Mutex
	ingestionRunning bool
}

// New creates a control Service for the given orchestrator.
func New(orch *orchestrator.Orchestrator) *Service {
	return &Service{orchestrator: orch}
}

// Status returns the current run state.
func (s *Service) Status() Status {
	orch := s.orchestrator.Status()
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		OrchestratorRunning:  orch.Running,
		OrchestratorCaughtUp: orch.CaughtUp,
		IngestionRunning:     s.ingestionRunning,
	}
}

// StartOrchestrator begins (or resumes) the simulated timeline.
func (s *Service) StartOrchestrator() (Status, error) {
	err := s.orchestrator.Start()
	return s.Status(), err
}

// StopOrchestrator pauses the simulated timeline.
func (s *Service) StopOrchestrator() (Status, error) {
	err := s.orchestrator.Stop()
	return s.Status(), err
}

// StartIngestion marks the ingestion worker as running.
func (s *Service) StartIngestion() (Status, error) {
	return s.setIngestion(true), nil
}

// StopIngestion marks the ingestion worker as stopped.
func (s *Service) StopIngestion() (Status, error) {
	return s.setIngestion(false), nil
}

func (s *Service) setIngestion(running bool) Status {
	s.mu.Lock()
	s.ingestionRunning = running
	s.mu.Unlock()
	return s.Status()
}
