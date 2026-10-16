package httpapi

import "testing"

func TestCSVFormulaInjectionEscaped(t *testing.T) {
	for _, v := range []string{"=SUM(A1)", "  @command", "+1", "-1", "\t=cmd"} {
		if spreadsheetText(v) == v {
			t.Fatalf("unsafe CSV cell %q", v)
		}
	}
	if spreadsheetText("客户甲") != "客户甲" {
		t.Fatal("normal text changed")
	}
}
