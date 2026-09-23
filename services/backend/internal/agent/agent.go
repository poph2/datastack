// Package agent will own natural-language question answering against ClickHouse via Ollama.
package agent

// Answer is a placeholder response shape for a natural-language question.
type Answer struct {
	Question string `json:"question"`
	SQL      string `json:"sql"`
	Rows     []any  `json:"rows"`
}

// Service will talk to Ollama and execute read-only ClickHouse queries.
type Service struct{}

// New creates an agent Service.
func New() *Service {
	return &Service{}
}

// Ask is not implemented yet; it will generate and run safe analytical SQL.
func (s *Service) Ask(question string) (*Answer, error) {
	return &Answer{Question: question}, nil
}
