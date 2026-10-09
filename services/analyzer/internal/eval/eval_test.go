package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/services/analyzer/internal/analyzer"
)

var tol = Tolerance{Abs: 0.01, Rel: 0.005}

func TestCasesAreValid(t *testing.T) {
	cases, err := Cases()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(cases), 15)

	for _, c := range cases {
		_, err := analyzer.CheckSQL(c.SQL)
		assert.NoError(t, err, "reference SQL of %s must pass the guard", c.ID)
	}
}

func TestCompare(t *testing.T) {
	tests := map[string]struct {
		got, want [][]any
		ordered   bool
		pass      bool
	}{
		"equal": {
			got: [][]any{{"GOLD", "10.00"}}, want: [][]any{{"GOLD", "10.00"}}, pass: true,
		},
		"different row order": {
			got:  [][]any{{"SILVER", json.Number("2")}, {"GOLD", json.Number("1")}},
			want: [][]any{{"GOLD", int64(1)}, {"SILVER", int64(2)}}, pass: true,
		},
		"row order matters when ordered": {
			got:  [][]any{{"SILVER", 2}, {"GOLD", 1}},
			want: [][]any{{"GOLD", 1}, {"SILVER", 2}}, ordered: true, pass: false,
		},
		"different column order and names": {
			got: [][]any{{"10.004", "GOLD"}}, want: [][]any{{"GOLD", "10.00"}}, pass: true,
		},
		"extra column": {
			got: [][]any{{"GOLD", "10.00", json.Number("3")}}, want: [][]any{{"GOLD", "10.00"}}, pass: true,
		},
		"missing column": {
			got: [][]any{{"GOLD"}}, want: [][]any{{"GOLD", "10.00"}}, pass: false,
		},
		"absolute tolerance": {
			got: [][]any{{json.Number("0.374")}}, want: [][]any{{"0.37"}}, pass: true,
		},
		"relative tolerance for live drift": {
			got: [][]any{{"1000100.00"}}, want: [][]any{{"1000000.00"}}, pass: true,
		},
		"too different": {
			got: [][]any{{"1100000.00"}}, want: [][]any{{"1000000.00"}}, pass: false,
		},
		"percent is not a fraction": {
			got: [][]any{{json.Number("83.5")}}, want: [][]any{{"0.84"}}, pass: false,
		},
		"null matches null only": {
			got: [][]any{{nil, 1}}, want: [][]any{{nil, 1}}, pass: true,
		},
		"null is not zero": {
			got: [][]any{{json.Number("0")}}, want: [][]any{{nil}}, pass: false,
		},
		"row count": {
			got: [][]any{{1}}, want: [][]any{{1}, {2}}, pass: false,
		},
		"each value used once": {
			got: [][]any{{"A", "x"}}, want: [][]any{{"A", "A"}}, pass: false,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			pass, reason := Compare(tt.got, tt.want, tt.ordered, tol)
			assert.Equal(t, tt.pass, pass, reason)
			if !tt.pass {
				assert.NotEmpty(t, reason)
			}
		})
	}
}

type fakeAsker struct {
	answers map[string]*analyzer.Answer
	err     error
}

func (f fakeAsker) Ask(_ context.Context, question, _ string) (*analyzer.Answer, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.answers[question], nil
}

type fakeQuerier map[string]*analyzer.Result

func (f fakeQuerier) Query(_ context.Context, query string, _ int) (*analyzer.Result, error) {
	result, ok := f[query]
	if !ok {
		return nil, errors.New("unknown reference")
	}
	return result, nil
}

func TestRunnerScoresAndReports(t *testing.T) {
	cases := []Case{
		{ID: "right", Question: "q1", SQL: "SELECT 1"},
		{ID: "wrong", Question: "q2", SQL: "SELECT 2"},
	}
	asker := fakeAsker{answers: map[string]*analyzer.Answer{
		"q1": {SQL: "SELECT one", Attempts: 1, Result: &analyzer.Result{Rows: [][]any{{json.Number("1")}}}},
		"q2": {SQL: "SELECT three", Attempts: 2, Result: &analyzer.Result{Rows: [][]any{{json.Number("3")}}}},
	}}
	reference := fakeQuerier{
		"SELECT 1": {Rows: [][]any{{json.Number("1")}}},
		"SELECT 2": {Rows: [][]any{{json.Number("2")}}},
	}

	var seen []string
	outcomes := NewRunner(asker, reference, "m", tol).Run(context.Background(), cases, func(o Outcome) {
		seen = append(seen, o.Case.ID)
	})

	assert.Equal(t, []string{"right", "wrong"}, seen)
	require.Len(t, outcomes, 2)
	assert.True(t, outcomes[0].Passed)
	assert.False(t, outcomes[1].Passed)
	assert.Equal(t, 2, outcomes[1].Attempts)
	assert.InDelta(t, 0.5, Score(outcomes), 1e-9)

	var report bytes.Buffer
	require.NoError(t, WriteReport(&report, "m", outcomes))
	assert.Contains(t, report.String(), "--- wrong: q2")
	assert.Contains(t, report.String(), "SELECT three")
	assert.Contains(t, report.String(), "model m: 1/2 passed, score 0.50")
}

func TestRunnerRecordsAskFailures(t *testing.T) {
	asker := fakeAsker{err: &analyzer.AskError{Code: analyzer.CodeQueryFailed, Message: "bad column", SQL: "SELECT x", Attempts: 2}}

	outcomes := NewRunner(asker, fakeQuerier{}, "m", tol).Run(context.Background(), []Case{{ID: "c", Question: "q", SQL: "SELECT 1"}}, nil)

	require.Len(t, outcomes, 1)
	assert.False(t, outcomes[0].Passed)
	assert.Equal(t, "QUERY_FAILED: bad column", outcomes[0].Reason)
	assert.Equal(t, "SELECT x", outcomes[0].SQL)
	assert.Equal(t, 2, outcomes[0].Attempts)
}
