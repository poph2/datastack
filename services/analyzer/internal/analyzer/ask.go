package analyzer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Error codes of a failed question, returned in the shared error body.
const (
	CodeUnsafeSQL       = "UNSAFE_SQL"
	CodeQueryFailed     = "QUERY_FAILED"
	CodeNoSQL           = "NO_SQL"
	CodeLLMUnavailable  = "LLM_UNAVAILABLE"
	CodeSchemaNotLoaded = "SCHEMA_NOT_LOADED"
)

// ErrUnknownModel means the request named a model the provider doesn't have.
var ErrUnknownModel = errors.New("unknown model")

// Database runs generated SQL as the read-only user. *StarRocks implements it.
type Database interface {
	Explain(ctx context.Context, query string) error
	Query(ctx context.Context, query string, maxRows int) (*Result, error)
}

// Schemas returns the current schema. *SchemaStore implements it.
type Schemas interface {
	Current() (*Schema, error)
}

// AskConfig bounds one question.
type AskConfig struct {
	// MaxAttempts counts the first generation plus repairs.
	MaxAttempts int
	MaxRows     int
}

// Answer is a question answered with data.
type Answer struct {
	Question string
	Model    string
	SQL      string
	Result   *Result
	Attempts int
	Duration time.Duration
}

// AskError is a question that produced no answer. SQL is the last query
// tried, if any, so a caller can show what went wrong.
type AskError struct {
	Code     string
	Message  string
	SQL      string
	Attempts int
	Err      error
}

func (e *AskError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AskError) Unwrap() error { return e.Err }

// Asker turns questions into answers: generate, guard, explain, execute,
// and on failure feed the error back to the model for another attempt.
type Asker struct {
	schemas   Schemas
	generator Generator
	db        Database
	config    AskConfig
	logger    *slog.Logger
}

// NewAsker returns an Asker.
func NewAsker(schemas Schemas, generator Generator, db Database, config AskConfig, logger *slog.Logger) (*Asker, error) {
	if config.MaxAttempts < 1 {
		return nil, fmt.Errorf("max attempts must be at least 1, got %d", config.MaxAttempts)
	}
	if config.MaxRows < 1 {
		return nil, fmt.Errorf("max rows must be at least 1, got %d", config.MaxRows)
	}
	return &Asker{schemas: schemas, generator: generator, db: db, config: config, logger: logger}, nil
}

// Generator returns the LLM provider, for GET /models.
func (a *Asker) Generator() Generator { return a.generator }

// Ask answers question with model ("" for the provider's default). It
// returns ErrUnknownModel, ErrSchemaNotLoaded or an *AskError on failure.
func (a *Asker) Ask(ctx context.Context, question, model string) (*Answer, error) {
	start := time.Now()
	model, err := a.resolveModel(ctx, model)
	if err != nil {
		return nil, err
	}

	answer, err := a.answer(ctx, question, model)
	a.record(ctx, question, model, answer, err)
	if err != nil {
		return nil, err
	}
	answer.Duration = time.Since(start)
	return answer, nil
}

// warmUpQuestion is asked by WarmUp. Any question works: the point is the
// system prompt in front of it.
const warmUpQuestion = "How many customers are there?"

// WarmUp generates one answer with the default model and throws it away.
// A local model keeps the processed system prompt in its cache, and that
// prompt is the same for every question, so after a warm-up a question only
// costs its own few tokens. Without one, the first person to ask waits for
// the whole schema prompt (minutes on a CPU-only laptop).
func (a *Asker) WarmUp(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "analyzer.warm_up")
	defer span.End()

	system, err := a.systemPrompt(ctx)
	if err != nil {
		return err
	}
	req := GenerateRequest{
		Model:    a.generator.DefaultModel(),
		System:   system,
		Messages: []Message{{Role: RoleUser, Content: questionPrompt(warmUpQuestion)}},
	}
	_, err = a.generate(ctx, req, 0)
	return err
}

func (a *Asker) resolveModel(ctx context.Context, model string) (string, error) {
	if model == "" || model == a.generator.DefaultModel() {
		return a.generator.DefaultModel(), nil
	}
	models, err := a.generator.Models(ctx)
	if err != nil {
		return "", &AskError{Code: CodeLLMUnavailable, Message: err.Error(), Err: err}
	}
	if !slices.Contains(models, model) {
		return "", fmt.Errorf("%w %q: choose one of %s", ErrUnknownModel, model, strings.Join(models, ", "))
	}
	return model, nil
}

func (a *Asker) answer(ctx context.Context, question, model string) (*Answer, error) {
	system, err := a.systemPrompt(ctx)
	if err != nil {
		return nil, err
	}

	messages := []Message{{Role: RoleUser, Content: questionPrompt(question)}}
	var failure *AskError
	for attempt := 1; attempt <= a.config.MaxAttempts; attempt++ {
		reply, err := a.generate(ctx, GenerateRequest{Model: model, System: system, Messages: messages}, attempt)
		if err != nil {
			return nil, &AskError{Code: CodeLLMUnavailable, Message: err.Error(), SQL: lastSQL(failure), Attempts: attempt, Err: err}
		}

		sql, result, failed := a.run(ctx, reply)
		if failed == nil {
			return &Answer{Question: question, Model: model, SQL: sql, Result: result, Attempts: attempt}, nil
		}
		failed.Attempts = attempt
		failure = failed
		if ctx.Err() != nil {
			break
		}
		messages = append(messages,
			Message{Role: RoleAssistant, Content: reply},
			Message{Role: RoleUser, Content: repairPrompt(failed.SQL, failed.Message)})
	}
	return nil, failure
}

func lastSQL(failure *AskError) string {
	if failure == nil {
		return ""
	}
	return failure.SQL
}

func (a *Asker) systemPrompt(ctx context.Context) (string, error) {
	_, span := tracer.Start(ctx, "schema.prompt")
	defer span.End()

	schema, err := a.schemas.Current()
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	prompt := SystemPrompt(schema)
	span.SetAttributes(
		attribute.Int("analyzer.schema.tables", len(schema.Tables)),
		attribute.Int("analyzer.prompt.chars", len(prompt)),
		attribute.String("analyzer.schema.loaded_at", schema.LoadedAt.Format(time.RFC3339)),
	)
	return prompt, nil
}

func (a *Asker) generate(ctx context.Context, req GenerateRequest, attempt int) (string, error) {
	provider := a.generator.Provider()
	ctx, span := tracer.Start(ctx, "llm.generate", trace.WithAttributes(
		attribute.String("llm.provider", provider),
		attribute.String("llm.model", req.Model),
		attribute.Int("analyzer.attempt", attempt),
	))
	defer span.End()

	start := time.Now()
	reply, err := a.generator.Generate(ctx, req)
	llmDuration.WithLabelValues(provider, req.Model).Observe(time.Since(start).Seconds())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}
	span.SetAttributes(attribute.Int("llm.reply.chars", len(reply)))
	return reply, nil
}

// run takes one model reply through extraction, the guard, EXPLAIN and
// execution. A failure carries the SQL and a message meant for the model.
func (a *Asker) run(ctx context.Context, reply string) (string, *Result, *AskError) {
	sql, err := ExtractSQL(reply)
	if err != nil {
		return "", nil, &AskError{Code: CodeNoSQL, Message: err.Error(), Err: err}
	}

	sql, err = a.guard(ctx, sql)
	if err != nil {
		return sql, nil, &AskError{Code: CodeUnsafeSQL, Message: err.Error(), SQL: sql, Err: err}
	}

	if err := a.explain(ctx, sql); err != nil {
		return sql, nil, &AskError{Code: CodeQueryFailed, Message: err.Error(), SQL: sql, Err: err}
	}

	result, err := a.execute(ctx, sql)
	if err != nil {
		return sql, nil, &AskError{Code: CodeQueryFailed, Message: err.Error(), SQL: sql, Err: err}
	}
	return sql, result, nil
}

func (a *Asker) guard(ctx context.Context, sql string) (string, error) {
	_, span := tracer.Start(ctx, "sql.guard")
	defer span.End()

	checked, err := CheckSQL(sql)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return sql, err
	}
	return checked, nil
}

func (a *Asker) explain(ctx context.Context, sql string) error {
	ctx, span := tracer.Start(ctx, "sql.explain", trace.WithAttributes(attribute.String("db.query.text", sql)))
	defer span.End()

	if err := a.db.Explain(ctx, sql); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (a *Asker) execute(ctx context.Context, sql string) (*Result, error) {
	ctx, span := tracer.Start(ctx, "sql.execute", trace.WithAttributes(attribute.String("db.query.text", sql)))
	defer span.End()

	result, err := a.db.Query(ctx, sql, a.config.MaxRows)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Int("db.response.returned_rows", len(result.Rows)), attribute.Bool("analyzer.truncated", result.Truncated))
	return result, nil
}

// record updates the metrics and logs the outcome of one question.
func (a *Asker) record(ctx context.Context, question, model string, answer *Answer, err error) {
	outcome, attempts, sql := outcomeSuccess, 0, ""
	if answer != nil {
		attempts, sql = answer.Attempts, answer.SQL
	}
	var failure *AskError
	switch {
	case errors.As(err, &failure):
		outcome, attempts, sql = strings.ToLower(failure.Code), failure.Attempts, failure.SQL
	case err != nil:
		outcome = strings.ToLower(CodeSchemaNotLoaded)
	}

	asksTotal.WithLabelValues(outcome, model).Inc()
	if attempts > 0 {
		askAttempts.WithLabelValues(outcome).Observe(float64(attempts))
	}
	a.logger.InfoContext(ctx, "question answered",
		"outcome", outcome, "model", model, "attempts", attempts, "question", question, "sql", sql)
}
