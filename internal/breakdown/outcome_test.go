package breakdown

import (
	"errors"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
)

func TestBreakdownRejectsMissingOutcome(t *testing.T) {
	registry := map[string]*model.CommandRules{
		"wrapper": {
			Breakdown: func(
				model.ParseResult,
				*model.State,
			) (model.BreakdownOutcome, error) {
				return model.BreakdownOutcome{}, nil
			},
		},
	}

	_, err := Breakdown(
		"wrapper", "/work", registry, model.RuleConfigs{})
	if err == nil || !strings.Contains(err.Error(), "returned no outcome") {
		t.Fatalf("missing outcome error = %v", err)
	}
}

func TestBreakdownAcceptsMissingOutcomeWithError(t *testing.T) {
	want := errors.New("cannot verify")
	registry := map[string]*model.CommandRules{
		"wrapper": {
			Breakdown: func(
				model.ParseResult,
				*model.State,
			) (model.BreakdownOutcome, error) {
				return model.BreakdownOutcome{}, want
			},
		},
	}

	_, err := Breakdown(
		"wrapper", "/work", registry, model.RuleConfigs{})
	if !errors.Is(err, want) {
		t.Fatalf("breakdown error = %v, want %v", err, want)
	}
}
