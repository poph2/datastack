package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/poph2/datastack/services/analyzer/internal/eval"
)

const (
	flagEvalModel   = "eval-model"
	flagMinScore    = "min-score"
	flagEvalCases   = "cases"
	flagAbsTolerant = "abs-tolerance"
	flagRelTolerant = "rel-tolerance"
)

func newEvalCommand(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Score the analyzer on the embedded question set against reference SQL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEval(cmd.Context(), v)
		},
	}

	flags := cmd.Flags()
	addPipelineFlags(flags)
	flags.String(flagEvalModel, "", "model to evaluate (empty: the provider's default)")
	flags.Float64(flagMinScore, 0, "exit non-zero when the score (0 to 1) is below this")
	flags.StringSlice(flagEvalCases, nil, "only run these case ids (default: all)")
	flags.Float64(flagAbsTolerant, 0.01, "absolute tolerance when comparing numbers")
	flags.Float64(flagRelTolerant, 0.005, "relative tolerance when comparing numbers, for data that changes under live load")
	return cmd
}

func runEval(ctx context.Context, v *viper.Viper) error {
	cases, err := selectCases(v.GetStringSlice(flagEvalCases))
	if err != nil {
		return err
	}

	p, err := newPipeline(ctx, v)
	if err != nil {
		return err
	}
	defer p.shutdown()

	if err := p.schemas.Refresh(ctx); err != nil {
		return fmt.Errorf("loading the schema: %w", err)
	}

	model := v.GetString(flagEvalModel)
	if model == "" {
		model = p.asker.Generator().DefaultModel()
	}
	fmt.Fprintf(os.Stderr, "evaluating %d questions with %s/%s\n", len(cases), p.asker.Generator().Provider(), model)

	runner := eval.NewRunner(p.asker, p.starRocks, model, eval.Tolerance{
		Abs: v.GetFloat64(flagAbsTolerant),
		Rel: v.GetFloat64(flagRelTolerant),
	})
	outcomes := runner.Run(ctx, cases, func(o eval.Outcome) {
		verdict := "FAIL"
		if o.Passed {
			verdict = "pass"
		}
		fmt.Fprintf(os.Stderr, "  %-32s %s (%.1fs)\n", o.Case.ID, verdict, o.Duration.Seconds())
	})

	if err := eval.WriteReport(os.Stdout, model, outcomes); err != nil {
		return err
	}
	if score, minScore := eval.Score(outcomes), v.GetFloat64(flagMinScore); score < minScore {
		return fmt.Errorf("score %.2f is below --%s %.2f", score, flagMinScore, minScore)
	}
	return nil
}

// selectCases returns the embedded cases, narrowed to ids when given.
func selectCases(ids []string) ([]eval.Case, error) {
	cases, err := eval.Cases()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return cases, nil
	}

	byID := map[string]eval.Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	selected := make([]eval.Case, 0, len(ids))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown case %q", id)
		}
		selected = append(selected, c)
	}
	return selected, nil
}
