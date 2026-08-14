package model

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// FlagDef describes a named flag and how it is parsed.
type FlagDef struct {
	Name     string
	Arg      bool // consumes next arg as value
	Prefix   bool // value can be concatenated (-n5)
	Terminal bool // remaining args become positionals
}

// FlagPlacement describes where a command accepts flags relative to its
// positional arguments.
type FlagPlacement int

const (
	// InterspersedFlags allows flags after positional arguments until an
	// explicit "--".
	InterspersedFlags FlagPlacement = iota
	// LeadingFlagsOnly makes the first positional argument end flag
	// parsing.
	LeadingFlagsOnly
)

// FullParser classifies flags by their FlagDef. Unknown flags cause a parse
// error. Use NewFullParser to create.
type FullParser struct {
	flags      []FlagDef
	flagMap    map[string]FlagDef
	placement  FlagPlacement
	unknownMsg string
}

// NewFullParser creates a FullParser from a flag list and the command's
// flag-placement grammar.
func NewFullParser(
	flags []FlagDef,
	placement FlagPlacement,
	unknownReason string,
) *FullParser {
	if placement != InterspersedFlags &&
		placement != LeadingFlagsOnly {
		panic(fmt.Sprintf(
			"invalid flag placement: %d", placement))
	}

	parserFlags := slices.Clone(flags)
	slices.SortStableFunc(
		parserFlags,
		func(a, b FlagDef) int {
			return cmp.Compare(
				len(b.Name), len(a.Name))
		})

	p := &FullParser{
		flags:      parserFlags,
		flagMap:    make(map[string]FlagDef, len(parserFlags)),
		placement:  placement,
		unknownMsg: unknownReason,
	}
	for _, f := range parserFlags {
		if _, exists := p.flagMap[f.Name]; exists {
			panic(fmt.Sprintf(
				"duplicate flag definition: %s", f.Name))
		}

		p.flagMap[f.Name] = f
	}

	return p
}

func (p *FullParser) Parse(
	args []*syntax.Word,
) (ParseResult, error) {
	result := ParseResult{Raw: args}

	endOfFlags := false
	for i := 0; i < len(args); i++ {
		if word.DefinitelyEqual(args[i], "--") {
			endOfFlags = true
			continue
		}
		// Positional: after --, bare "-", or anything not starting with
		// "-".
		if endOfFlags ||
			word.DefinitelyEqual(args[i], "-") ||
			!word.DefinitelyHasPrefix(
				args[i], "-") {
			result.Positionals = append(
				result.Positionals, args[i])
			if p.placement == LeadingFlagsOnly {
				endOfFlags = true
			}

			continue
		}

		// Flag with =: split via SplitEq.
		name, valueWord := word.SplitEq(args[i])
		if valueWord != nil {
			if _, ok := p.flagMap[name]; !ok {
				return ParseResult{},
					fmt.Errorf("%s: %s",
						p.unknownReason(), name)
			}

			result.Flags = append(result.Flags,
				ParsedFlag{
					Name:  name,
					Value: valueWord,
				})
			continue
		}

		// Non-equals flag - resolve text for map lookup and prefix
		// matching.
		text := word.Text(args[i])

		// Exact match.
		if def, ok := p.flagMap[text]; ok {
			if def.Arg {
				if i+1 >= len(args) {
					return ParseResult{},
						fmt.Errorf(
							"flag %s requires "+
								"a value", text)
				}

				i++
				result.Flags = append(
					result.Flags,
					ParsedFlag{
						Name:  text,
						Value: args[i],
					})
			} else {
				result.Flags = append(
					result.Flags,
					ParsedFlag{
						Name: text})
			}

			if def.Terminal {
				endOfFlags = true
			}

			continue
		}

		// Prefix match: -n5 -> "-n", Word("5").
		if flagName, vw, ok :=
			p.matchPrefix(args[i]); ok {
			result.Flags = append(result.Flags,
				ParsedFlag{
					Name:  flagName,
					Value: vw,
				})
			continue
		}

		// Combined short flags: split -uBs into -u + -B + -s. Only for
		// static words. Opaque content can't be reliably split.
		if word.Static(args[i]) &&
			len(text) > 2 &&
			text[0] == '-' && text[1] != '-' {
			expanded, needsArg, ok :=
				p.splitCluster(text)
			if ok {
				result.Flags = append(
					result.Flags, expanded...)
				if needsArg {
					last := &result.Flags[len(result.Flags)-1]
					if i+1 >= len(args) {
						return ParseResult{},
							fmt.Errorf(
								"flag %s requires"+
									" a value",
								last.Name)
					}

					i++
					last.Value = args[i]
				}

				// A terminal flag in a cluster absorbs
				// remaining args, same as when parsed
				// standalone.
				lastName := result.Flags[len(
					result.Flags)-1].Name
				if def, ok :=
					p.flagMap[lastName]; ok &&
					def.Terminal {
					endOfFlags = true
				}

				continue
			}
		}

		return ParseResult{}, fmt.Errorf(
			"%s: %s", p.unknownReason(), text)
	}

	return result, nil
}

// splitCluster splits combined short flags (e.g. -uBs) into individual flags
// using greedy left-to-right matching. The constructor stores flags
// longest-first so -OO matches before -O. When an Arg or Prefix flag appears
// mid-cluster, the remaining characters become its value.
func (p *FullParser) splitCluster(
	text string,
) ([]ParsedFlag, bool, bool) {
	pos := 1 // skip leading "-"
	var flags []ParsedFlag
	for pos < len(text) {
		matched := false
		for _, sf := range p.flags {
			if !strings.HasPrefix(sf.Name, "-") ||
				strings.HasPrefix(sf.Name, "--") {
				continue
			}

			body := sf.Name[1:] // strip "-"
			if !strings.HasPrefix(
				text[pos:], body) {
				continue
			}

			pos += len(body)
			// Arg/Prefix flag with remaining chars: rest becomes
			// its value.
			if (sf.Arg || sf.Prefix) &&
				pos < len(text) {
				flags = append(flags,
					ParsedFlag{
						Name: sf.Name,
						Value: word.Lit(
							text[pos:]),
					})
				return flags, false, true
			}

			flags = append(flags,
				ParsedFlag{Name: sf.Name})
			// Arg flag at end of cluster: caller must consume next
			// arg.
			if sf.Arg {
				return flags, true, true
			}

			matched = true
			break
		}

		if !matched {
			return nil, false, false
		}
	}

	return flags, false, true
}

// matchPrefix checks if arg starts with a known Prefix flag. Returns the flag
// name, a value Word, and whether a match was found.
func (p *FullParser) matchPrefix(
	w *syntax.Word,
) (string, *syntax.Word, bool) {
	for _, f := range p.flags {
		if !f.Prefix {
			continue
		}
		if word.DefinitelyHasPrefix(w, f.Name) &&
			!word.DefinitelyEqual(w, f.Name) {
			_, valueWord := word.SplitPrefix(
				w, len(f.Name))
			if valueWord != nil {
				return f.Name, valueWord, true
			}
		}
	}

	return "", nil, false
}

func (p *FullParser) unknownReason() string {
	if p.unknownMsg != "" {
		return p.unknownMsg
	}

	return "unrecognised flag"
}
