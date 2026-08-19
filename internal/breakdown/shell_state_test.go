package breakdown

import "testing"

func TestChildShellsInheritFunctionsWithoutLeakingChanges(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{
			"subshell",
			`known(){ :; }; (known; inner(){ :; }); inner`,
		},
		{
			"pipeline",
			`known(){ :; }; { known; inner(){ :; }; } | cat; inner`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := breakdownWithAllRules(t, test.command)
			if err != nil {
				t.Fatalf("breakdown error: %v", err)
			}

			known := commandFunctionFlags(result, "known")
			if len(known) != 1 || !known[0] {
				t.Errorf("known function flags = %v, want [true]", known)
			}

			inner := commandFunctionFlags(result, "inner")
			if len(inner) != 1 || inner[0] {
				t.Errorf("inner function flags = %v, want [false]", inner)
			}
		})
	}
}
