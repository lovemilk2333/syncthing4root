package main

import "testing"

func mustEval(t *testing.T, expr string, vals map[string]bool) bool {
	t.Helper()
	node, err := parseExpr(expr)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	return node.eval(vals)
}

func TestParseExpr_Empty(t *testing.T) {
	if !mustEval(t, "", nil) {
		t.Error("empty expr should be true")
	}
	if !mustEval(t, "   ", nil) {
		t.Error("whitespace expr should be true")
	}
}

func TestEval_Basic(t *testing.T) {
	v := map[string]bool{"a": true, "b": false}
	cases := map[string]bool{
		"a":            true,
		"b":            false,
		"NOT a":        false,
		"NOT b":        true,
		"a AND b":      false,
		"a OR b":       true,
		"a AND NOT b":  true,
		"NOT (a AND b)": true,
	}
	for expr, want := range cases {
		if got := mustEval(t, expr, v); got != want {
			t.Errorf("%q = %v, want %v", expr, got, want)
		}
	}
}

func TestEval_Precedence(t *testing.T) {
	// AND binds tighter than OR: "a OR b AND c" == "a OR (b AND c)"
	v := map[string]bool{"a": true, "b": false, "c": false}
	if !mustEval(t, "a OR b AND c", v) {
		t.Error("a OR (b AND c) should be true when a=true")
	}
	// NOT binds tighter than AND: "NOT a AND b" == "(NOT a) AND b"
	v2 := map[string]bool{"a": false, "b": true}
	if !mustEval(t, "NOT a AND b", v2) {
		t.Error("(NOT a) AND b should be true")
	}
}

func TestEval_NestedParens(t *testing.T) {
	v := map[string]bool{"w": true, "c": false, "p": true, "x": false}
	// w AND (p OR (NOT c AND x))
	if !mustEval(t, "w AND (p OR (NOT c AND x))", v) {
		t.Error("nested expr should be true")
	}
}

func TestEval_Synonyms(t *testing.T) {
	v := map[string]bool{"a": true, "b": false}
	if !mustEval(t, "a && !b", v) {
		t.Error("&& and ! synonyms failed")
	}
	if !mustEval(t, "b || a", v) {
		t.Error("|| synonym failed")
	}
}

func TestEval_CaseInsensitive(t *testing.T) {
	v := map[string]bool{"a": true, "b": true}
	if !mustEval(t, "a and b", v) || !mustEval(t, "a Or b", v) {
		t.Error("keywords should be case-insensitive")
	}
}

func TestParseExpr_Errors(t *testing.T) {
	bad := []string{
		"a AND",
		"AND a",
		"a b",
		"(a",
		"a)",
		"NOT",
		"a OR OR b",
		"@",
	}
	for _, expr := range bad {
		if _, err := parseExpr(expr); err == nil {
			t.Errorf("expected parse error for %q", expr)
		}
	}
}

func TestValidateExpr(t *testing.T) {
	known := []string{"wifi", "cellular", "power", "probe"}
	if err := validateExpr("wifi AND NOT cellular", known); err != nil {
		t.Errorf("valid expr rejected: %v", err)
	}
	if err := validateExpr("", known); err != nil {
		t.Errorf("empty expr should be valid: %v", err)
	}
	if err := validateExpr("wifi AND unknownterm", known); err == nil {
		t.Error("unknown identifier should be rejected")
	}
	if err := validateExpr("wifi AND", known); err == nil {
		t.Error("syntax error should be rejected")
	}
}
