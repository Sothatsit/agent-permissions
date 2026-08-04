package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

var envParser, _envBaseBreakdown = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--ignore-environment"},
			{Name: "--split-string", Arg: true},
			{Name: "--version"},
			{Name: "--unset", Arg: true},
			{Name: "--chdir", Arg: true},
			{Name: "--debug"},
			{Name: "--help"},
			{Name: "--null"},
			{Name: "-S", Arg: true, Prefix: true},
			{Name: "-C", Arg: true, Prefix: true},
			{Name: "-u", Arg: true, Prefix: true},
			{Name: "-i"},
			{Name: "-0"},
		},
		denyFlags: map[string]string{
			"-S": "env -S/--split-string runs env's own " +
				"string splitter, whose semantics differ " +
				"from the shell — cannot verify what runs",
			"--split-string": "env -S/--split-string runs " +
				"env's own string splitter, whose semantics " +
				"differ from the shell — cannot verify what " +
				"runs",
		},
		denyRule: envUnverified,
	})

// breakdownEnv unwraps env, the one wrapper that honours a
// leading NAME=val: it sets those variables in the inner
// command's environment, so each name must reach the EnvVars
// deny axis (env LD_PRELOAD=/x cmd is a real injection) and the
// inner command must be re-analysed. The base wrapper parses
// env's flags and denies -S; here we split the positionals into
// the honoured assignments and the inner command. Bash's own
// assignment rule applies: leading words containing '=' are
// assignments; the first word without one is the command.
func breakdownEnv(
	input model.ParseResult,
	state *model.State,
) (*model.UnwrapResult, error) {
	result, err := _envBaseBreakdown(input, state)
	if err != nil || result == nil ||
		len(result.Commands) == 0 {
		// Error, declined, or bare env (no command).
		return result, err
	}

	words := result.Commands[0]
	var assigns []*syntax.Assign
	i := 0
	for i < len(words) {
		// A bare "-" is the old spelling of -i (ignore
		// environment), not an assignment or the command.
		if word.DefinitelyEqual(words[i], "-") {
			i++
			continue
		}
		name, value := word.SplitEq(words[i])
		if value == nil {
			break // first word without '=' is the command
		}
		assigns = append(assigns, &syntax.Assign{
			Name:  &syntax.Lit{Value: name},
			Value: value,
		})
		i++
	}

	out := &model.UnwrapResult{Assigns: assigns}
	if rest := words[i:]; len(rest) > 0 {
		out.Commands = [][]*syntax.Word{rest}
	}
	return out, nil
}
