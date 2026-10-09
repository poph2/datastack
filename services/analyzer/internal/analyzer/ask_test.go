package analyzer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goodSQL = "SELECT loyalty_tier, SUM(total_amount) AS revenue FROM orders GROUP BY loyalty_tier"

func newTestAsker(t *testing.T, generator Generator, db Database, maxAttempts int) *Asker {
	t.Helper()
	asker, err := NewAsker(loadedSchemas(), generator, db, AskConfig{MaxAttempts: maxAttempts, MaxRows: 500}, discardLogger)
	require.NoError(t, err)
	return asker
}

func TestAskAnswersOnFirstAttempt(t *testing.T) {
	generator := &fakeGenerator{replies: []string{"```sql\n" + goodSQL + ";\n```"}}
	db := &fakeDatabase{result: oneRowResult()}

	answer, err := newTestAsker(t, generator, db, 2).Ask(context.Background(), "Revenue by tier?", "")
	require.NoError(t, err)

	assert.Equal(t, "Revenue by tier?", answer.Question)
	assert.Equal(t, "default-model", answer.Model)
	assert.Equal(t, goodSQL, answer.SQL)
	assert.Equal(t, 1, answer.Attempts)
	assert.Equal(t, oneRowResult(), answer.Result)
	assert.Equal(t, []string{goodSQL}, db.executed)

	require.Len(t, generator.requests, 1)
	req := generator.requests[0]
	assert.Equal(t, "default-model", req.Model)
	assert.Contains(t, req.System, "TABLE orders")
	assert.Equal(t, []Message{{Role: RoleUser, Content: "Question: Revenue by tier?\nSQL:"}}, req.Messages)
}

func TestAskRepairsAQueryThatFailsExplain(t *testing.T) {
	badSQL := "SELECT tier FROM orders"
	generator := &fakeGenerator{replies: []string{badSQL, goodSQL}}
	db := &fakeDatabase{
		explainErrors: map[string]error{badSQL: errors.New("Unknown column 'tier'")},
		result:        oneRowResult(),
	}

	answer, err := newTestAsker(t, generator, db, 2).Ask(context.Background(), "Revenue by tier?", "")
	require.NoError(t, err)
	assert.Equal(t, 2, answer.Attempts)
	assert.Equal(t, goodSQL, answer.SQL)

	// The repair turn shows the model its query and the error.
	require.Len(t, generator.requests, 2)
	repair := generator.requests[1].Messages
	require.Len(t, repair, 3)
	assert.Equal(t, Message{Role: RoleAssistant, Content: badSQL}, repair[1])
	assert.Equal(t, RoleUser, repair[2].Role)
	assert.Contains(t, repair[2].Content, badSQL)
	assert.Contains(t, repair[2].Content, "Unknown column 'tier'")
}

func TestAskRepairsAnExecutionError(t *testing.T) {
	slowSQL := "SELECT COUNT(*) FROM orders"
	generator := &fakeGenerator{replies: []string{slowSQL, goodSQL}}
	db := &fakeDatabase{
		queryErrors: map[string]error{slowSQL: errors.New("query timeout")},
		result:      oneRowResult(),
	}

	answer, err := newTestAsker(t, generator, db, 2).Ask(context.Background(), "q", "")
	require.NoError(t, err)
	assert.Equal(t, 2, answer.Attempts)
	assert.Equal(t, []string{slowSQL, goodSQL}, db.executed)
}

func TestAskGivesUpAfterMaxAttempts(t *testing.T) {
	badSQL := "SELECT nope FROM orders"
	generator := &fakeGenerator{replies: []string{badSQL}}
	db := &fakeDatabase{explainErrors: map[string]error{badSQL: errors.New("Unknown column 'nope'")}}

	_, err := newTestAsker(t, generator, db, 3).Ask(context.Background(), "q", "")

	var failure *AskError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, CodeQueryFailed, failure.Code)
	assert.Equal(t, badSQL, failure.SQL)
	assert.Equal(t, 3, failure.Attempts)
	assert.Contains(t, failure.Message, "Unknown column 'nope'")
	assert.Len(t, generator.requests, 3)
	assert.Empty(t, db.executed)
}

func TestAskFailureCodes(t *testing.T) {
	tests := map[string]struct {
		reply   string
		code    string
		wantSQL string
	}{
		"no sql":                   {reply: "I cannot answer that.", code: CodeNoSQL, wantSQL: ""},
		"second statement dropped": {reply: "SELECT 1; DROP TABLE orders", code: CodeQueryFailed, wantSQL: "SELECT 1"},
		"write":                    {reply: "WITH x AS (SELECT 1) DELETE FROM orders", code: CodeUnsafeSQL, wantSQL: "WITH x AS (SELECT 1) DELETE FROM orders"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			db := &fakeDatabase{
				explainErrors: map[string]error{"SELECT 1": errors.New("explain refused")},
			}
			_, err := newTestAsker(t, &fakeGenerator{replies: []string{tt.reply}}, db, 1).Ask(context.Background(), "q", "")

			var failure *AskError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, tt.code, failure.Code)
			assert.Equal(t, tt.wantSQL, failure.SQL)
			assert.Empty(t, db.executed, "nothing unsafe or unplanned runs")
		})
	}
}

func TestAskLLMUnavailableIsNotRepaired(t *testing.T) {
	generator := &fakeGenerator{err: fmt.Errorf("%w: connection refused", ErrLLMUnavailable)}

	_, err := newTestAsker(t, generator, &fakeDatabase{}, 2).Ask(context.Background(), "q", "")

	var failure *AskError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, CodeLLMUnavailable, failure.Code)
	assert.ErrorIs(t, err, ErrLLMUnavailable)
	assert.Len(t, generator.requests, 1)
}

func TestAskModelSelection(t *testing.T) {
	generator := &fakeGenerator{replies: []string{goodSQL}, models: []string{"default-model", "other-model"}}
	asker := newTestAsker(t, generator, &fakeDatabase{result: oneRowResult()}, 1)

	answer, err := asker.Ask(context.Background(), "q", "other-model")
	require.NoError(t, err)
	assert.Equal(t, "other-model", answer.Model)
	assert.Equal(t, "other-model", generator.requests[0].Model)

	_, err = asker.Ask(context.Background(), "q", "missing-model")
	assert.ErrorIs(t, err, ErrUnknownModel)
	assert.Contains(t, err.Error(), "default-model, other-model")
}

func TestAskWithoutSchema(t *testing.T) {
	asker, err := NewAsker(staticSchemas{err: ErrSchemaNotLoaded}, &fakeGenerator{replies: []string{goodSQL}},
		&fakeDatabase{}, AskConfig{MaxAttempts: 1, MaxRows: 1}, discardLogger)
	require.NoError(t, err)

	_, err = asker.Ask(context.Background(), "q", "")
	assert.ErrorIs(t, err, ErrSchemaNotLoaded)
}

func TestNewAskerValidatesConfig(t *testing.T) {
	_, err := NewAsker(loadedSchemas(), &fakeGenerator{}, &fakeDatabase{}, AskConfig{MaxAttempts: 0, MaxRows: 1}, discardLogger)
	require.Error(t, err)
	_, err = NewAsker(loadedSchemas(), &fakeGenerator{}, &fakeDatabase{}, AskConfig{MaxAttempts: 1, MaxRows: 0}, discardLogger)
	require.Error(t, err)
}

func TestWarmUpSendsTheSystemPromptToTheDefaultModel(t *testing.T) {
	generator := &fakeGenerator{replies: []string{"SELECT COUNT(*) FROM customers"}}
	db := &fakeDatabase{}

	require.NoError(t, newTestAsker(t, generator, db, 2).WarmUp(context.Background()))

	require.Len(t, generator.requests, 1)
	assert.Equal(t, "default-model", generator.requests[0].Model)
	assert.Equal(t, SystemPrompt(loadedSchemas().schema), generator.requests[0].System)
	assert.Empty(t, db.executed, "the warm-up reply is never run")
}
