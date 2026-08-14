package model

import "testing"

func TestBreakdownResultIsSafeWithCheckedAssignments(t *testing.T) {
	result := SafeBreakdown()
	if !result.IsSafe() {
		t.Fatal("empty verified result is not safe")
	}

	result.Assigns = append(result.Assigns, "FOO")
	if !result.IsSafe() {
		t.Fatal("result with checked assignment is not safe")
	}
}

func TestBreakdownResultWorkCannotRemainSafe(t *testing.T) {
	result := SafeBreakdown()
	result.Commands = append(result.Commands, Command{})
	if result.IsSafe() {
		t.Fatal("result with a command is safe")
	}
}
