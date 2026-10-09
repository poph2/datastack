package analyzer

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeRow(t *testing.T) {
	columns := []ResultColumn{
		{Name: "count", Type: "bigint"},
		{Name: "count_text", Type: "bigint"},
		{Name: "revenue", Type: "decimal"},
		{Name: "ratio", Type: "double"},
		{Name: "ratio_text", Type: "double"},
		{Name: "day", Type: "datetime"},
		{Name: "tier", Type: "varchar"},
		{Name: "missing", Type: "decimal"},
		{Name: "huge", Type: "largeint"},
		{Name: "nan", Type: "double"},
		{Name: "parsed_time", Type: "datetime"},
	}
	raw := []any{
		int64(42),
		[]byte("7"),
		[]byte("31590768.70"),
		float64(0.25),
		[]byte("1.3243770065739184"),
		[]byte("2026-10-09 13:18:01"),
		[]byte("GOLD"),
		nil,
		[]byte("170141183460469231731687303715884105727"),
		math.NaN(),
		time.Date(2026, 10, 9, 13, 18, 1, 0, time.UTC),
	}

	row := EncodeRow(columns, raw)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	assert.JSONEq(t, `[42, 7, "31590768.70", 0.25, 1.3243770065739184, "2026-10-09 13:18:01", "GOLD", null,
		"170141183460469231731687303715884105727", "NaN", "2026-10-09 13:18:01"]`, string(encoded))
}

func TestDecimalKeepsTrailingZeros(t *testing.T) {
	row := EncodeRow([]ResultColumn{{Name: "amount", Type: "decimal"}}, []any{[]byte("50.00")})
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	assert.Equal(t, `["50.00"]`, string(encoded))
}
