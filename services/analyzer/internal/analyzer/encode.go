package analyzer

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// Result is a query result ready to serialise.
type Result struct {
	Columns   []ResultColumn
	Rows      [][]any
	Truncated bool
}

// ResultColumn names a result column and its lower-cased StarRocks type.
type ResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// dateTimeLayout is how StarRocks prints a DATETIME over the text protocol.
const dateTimeLayout = "2006-01-02 15:04:05"

// EncodeRow turns driver values into JSON-ready values: integers and floats
// become JSON numbers, DECIMAL stays the exact string StarRocks returned
// (money must never pass through float64), everything else becomes a string,
// and NULL becomes nil.
func EncodeRow(columns []ResultColumn, raw []any) []any {
	row := make([]any, len(raw))
	for i, value := range raw {
		row[i] = encodeValue(columns[i].Type, value)
	}
	return row
}

func encodeValue(columnType string, value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case int64:
		return v
	case uint64:
		return v
	case float64:
		return finiteNumber(v, strconv.FormatFloat(v, 'g', -1, 64))
	case float32:
		return finiteNumber(float64(v), strconv.FormatFloat(float64(v), 'g', -1, 32))
	case bool:
		return v
	case time.Time:
		return v.UTC().Format(dateTimeLayout)
	case []byte:
		return encodeText(columnType, string(v))
	case string:
		return encodeText(columnType, v)
	default:
		return v
	}
}

// encodeText types a value the text protocol delivered as bytes.
func encodeText(columnType, text string) any {
	switch {
	case isIntegerType(columnType):
		if _, err := strconv.ParseInt(text, 10, 64); err == nil {
			return json.Number(text)
		}
		// LARGEINT can exceed int64. Keep its digits exact as a string.
		return text
	case isFloatType(columnType):
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return finiteNumber(f, text)
		}
		return text
	default:
		return text
	}
}

// finiteNumber returns f as a JSON number, or its text when JSON can't hold
// it (NaN, ±Inf).
func finiteNumber(f float64, text string) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return text
	}
	return json.Number(text)
}

func isIntegerType(columnType string) bool {
	switch strings.ToLower(columnType) {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint":
		return true
	}
	return false
}

func isFloatType(columnType string) bool {
	switch strings.ToLower(columnType) {
	case "float", "double":
		return true
	}
	return false
}
