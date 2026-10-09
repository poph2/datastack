package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Tolerance decides when two numbers are equal. Abs absorbs rounding (the
// spec's 0.01). Rel absorbs live traffic: k6 keeps writing while the
// generated and the reference query run a moment apart, so counts and sums
// drift slightly between them.
type Tolerance struct {
	Abs float64
	Rel float64
}

// Compare reports whether got answers the same question as want. It compares
// values, not column names or column order: every value of a want row must
// appear in the matching got row, which may carry extra columns (a model
// that adds a helpful count still answers the question). Rows are matched in
// order when ordered is set, as a multiset otherwise. The reason explains a
// mismatch.
func Compare(got, want [][]any, ordered bool, tol Tolerance) (bool, string) {
	if len(got) != len(want) {
		return false, fmt.Sprintf("got %d rows, want %d", len(got), len(want))
	}
	if ordered {
		for i := range want {
			if !rowMatches(got[i], want[i], tol) {
				return false, fmt.Sprintf("row %d: got %s, want %s", i+1, formatRow(got[i]), formatRow(want[i]))
			}
		}
		return true, ""
	}

	used := make([]bool, len(got))
	for _, wantRow := range want {
		match := -1
		for i, gotRow := range got {
			if !used[i] && rowMatches(gotRow, wantRow, tol) {
				match = i
				break
			}
		}
		if match < 0 {
			return false, fmt.Sprintf("no row matches %s", formatRow(wantRow))
		}
		used[match] = true
	}
	return true, ""
}

// rowMatches reports whether every value of want has a distinct equal value
// in got.
func rowMatches(got, want []any, tol Tolerance) bool {
	if len(got) < len(want) {
		return false
	}
	used := make([]bool, len(got))
	for _, w := range want {
		found := false
		for i, g := range got {
			if !used[i] && valuesEqual(g, w, tol) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func valuesEqual(a, b any, tol Tolerance) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	fa, aNumeric := number(a)
	fb, bNumeric := number(b)
	if aNumeric && bNumeric {
		return numbersEqual(fa, fb, tol)
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func numbersEqual(a, b float64, tol Tolerance) bool {
	diff := math.Abs(a - b)
	if diff <= tol.Abs {
		return true
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	return diff <= tol.Rel*scale
}

// number reads a value as a float: JSON numbers, Go numbers, and strings
// such as DECIMAL values that hold one.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func formatRow(row []any) string {
	parts := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			parts[i] = "NULL"
			continue
		}
		parts[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
