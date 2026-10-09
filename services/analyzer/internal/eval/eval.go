// Package eval measures how well the analyzer answers a fixed set of
// questions: each question's generated answer is compared with the result
// of a hand-written reference query on the same live data.
package eval

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"go.opentelemetry.io/otel"
	"go.yaml.in/yaml/v3"

	"github.com/poph2/datastack/services/analyzer/internal/analyzer"
)

//go:embed questions.yaml
var questionsYAML []byte

// referenceMaxRows bounds a reference result. Every question has a small
// answer; this only guards against a broken reference query.
const referenceMaxRows = 10000

var tracer = otel.Tracer("github.com/poph2/datastack/services/analyzer/eval")

// Case is one question with its reference SQL. Ordered is set when the
// question asks for an order (top N), so row order is compared too.
type Case struct {
	ID       string `yaml:"id"`
	Question string `yaml:"question"`
	SQL      string `yaml:"sql"`
	Ordered  bool   `yaml:"ordered"`
}

// Cases returns the embedded evaluation set.
func Cases() ([]Case, error) {
	var cases []Case
	if err := yaml.Unmarshal(questionsYAML, &cases); err != nil {
		return nil, fmt.Errorf("parsing questions.yaml: %w", err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || c.Question == "" || c.SQL == "" {
			return nil, fmt.Errorf("questions.yaml: case %q needs an id, a question and sql", c.ID)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("questions.yaml: duplicate id %q", c.ID)
		}
		seen[c.ID] = true
	}
	return cases, nil
}

// Asker answers a question (the same pipeline as POST /ask).
type Asker interface {
	Ask(ctx context.Context, question, model string) (*analyzer.Answer, error)
}

// Querier runs reference SQL.
type Querier interface {
	Query(ctx context.Context, query string, maxRows int) (*analyzer.Result, error)
}

// Outcome is the verdict on one case.
type Outcome struct {
	Case     Case
	Passed   bool
	Reason   string
	SQL      string
	Attempts int
	Duration time.Duration
}

// Runner evaluates cases one at a time.
type Runner struct {
	asker     Asker
	reference Querier
	model     string
	tolerance Tolerance
}

// NewRunner returns a Runner that asks model ("" for the default).
func NewRunner(asker Asker, reference Querier, model string, tolerance Tolerance) *Runner {
	return &Runner{asker: asker, reference: reference, model: model, tolerance: tolerance}
}

// Run evaluates every case, calling progress after each one.
func (r *Runner) Run(ctx context.Context, cases []Case, progress func(Outcome)) []Outcome {
	outcomes := make([]Outcome, 0, len(cases))
	for _, c := range cases {
		if ctx.Err() != nil {
			break
		}
		outcome := r.evaluate(ctx, c)
		outcomes = append(outcomes, outcome)
		if progress != nil {
			progress(outcome)
		}
	}
	return outcomes
}

// evaluate asks the question, then runs the reference straight after, so
// both see nearly the same data.
func (r *Runner) evaluate(ctx context.Context, c Case) Outcome {
	ctx, span := tracer.Start(ctx, "eval.case")
	defer span.End()

	start := time.Now()
	outcome := Outcome{Case: c}
	answer, err := r.asker.Ask(ctx, c.Question, r.model)
	outcome.Duration = time.Since(start)
	if err != nil {
		outcome.Reason, outcome.SQL, outcome.Attempts = describeFailure(err)
		return outcome
	}
	outcome.SQL, outcome.Attempts = answer.SQL, answer.Attempts

	want, err := r.reference.Query(ctx, c.SQL, referenceMaxRows)
	if err != nil {
		outcome.Reason = "reference query failed: " + err.Error()
		return outcome
	}
	if answer.Result.Truncated {
		outcome.Reason = "answer was truncated"
		return outcome
	}
	outcome.Passed, outcome.Reason = Compare(answer.Result.Rows, want.Rows, c.Ordered, r.tolerance)
	return outcome
}

func describeFailure(err error) (reason, sql string, attempts int) {
	var failure *analyzer.AskError
	if errors.As(err, &failure) {
		return failure.Code + ": " + failure.Message, failure.SQL, failure.Attempts
	}
	return err.Error(), "", 0
}

// Score is the fraction of outcomes that passed.
func Score(outcomes []Outcome) float64 {
	if len(outcomes) == 0 {
		return 0
	}
	passed := 0
	for _, o := range outcomes {
		if o.Passed {
			passed++
		}
	}
	return float64(passed) / float64(len(outcomes))
}

// WriteReport prints a pass/fail table, the details of every failure and the
// overall score.
func WriteReport(w io.Writer, model string, outcomes []Outcome) error {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "CASE\tRESULT\tATTEMPTS\tSECONDS\tQUESTION")
	for _, o := range outcomes {
		result := "FAIL"
		if o.Passed {
			result = "pass"
		}
		fmt.Fprintf(table, "%s\t%s\t%d\t%.1f\t%s\n", o.Case.ID, result, o.Attempts, o.Duration.Seconds(), o.Case.Question)
	}
	if err := table.Flush(); err != nil {
		return err
	}

	for _, o := range outcomes {
		if o.Passed {
			continue
		}
		fmt.Fprintf(w, "\n--- %s: %s\n    reason: %s\n    sql:    %s\n", o.Case.ID, o.Case.Question, o.Reason, oneLine(o.SQL))
	}

	passed := int(Score(outcomes)*float64(len(outcomes)) + 0.5)
	_, err := fmt.Fprintf(w, "\nmodel %s: %d/%d passed, score %.2f\n", model, passed, len(outcomes), Score(outcomes))
	return err
}

func oneLine(sql string) string {
	if sql == "" {
		return "(none)"
	}
	return strings.Join(strings.Fields(sql), " ")
}
