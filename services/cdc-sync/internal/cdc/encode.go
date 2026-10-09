package cdc

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Formats StarRocks parses for DATETIME and DATE columns. Timestamps are
// always converted to UTC first, so the session time zone never matters.
const (
	datetimeFormat = "2006-01-02 15:04:05.000000"
	dateFormat     = "2006-01-02"
)

// encodeRows turns a batch into the JSON array Stream Load takes
// (strip_outer_array), one object per row keyed by column name.
func encodeRows(batch Batch) ([]byte, error) {
	objects := make([]map[string]any, 0, len(batch.Rows))
	for _, row := range batch.Rows {
		object := make(map[string]any, len(batch.Columns))
		for i, column := range batch.Columns {
			object[column] = encodeValue(row[i], batch.TypeOIDs[i])
		}
		objects = append(objects, object)
	}

	body, err := json.Marshal(objects)
	if err != nil {
		return nil, fmt.Errorf("encoding rows as JSON: %w", err)
	}
	return body, nil
}

// encodeValue converts one pgx-decoded value into something json.Marshal
// renders the way StarRocks loads it.
func encodeValue(value any, typeOID uint32) any {
	switch v := value.(type) {
	case nil:
		return nil
	case time.Time:
		if typeOID == pgtype.DateOID {
			return v.Format(dateFormat)
		}
		return v.UTC().Format(datetimeFormat)
	case pgtype.Numeric:
		return numericString(v)
	case float64:
		return finiteOrNil(v)
	case float32:
		return finiteOrNil(float64(v))
	case [16]byte:
		return pgtype.UUID{Bytes: v, Valid: true}.String()
	default:
		return v
	}
}

// numericString renders a numeric as its exact decimal text. A JSON number
// would go through float64 in many decoders and lose cents. NaN and the
// infinities have no StarRocks DECIMAL equivalent, so they load as NULL.
func numericString(n pgtype.Numeric) any {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite {
		return nil
	}
	text, err := n.Value()
	if err != nil {
		return nil
	}
	return text
}

// finiteOrNil maps NaN and ±Inf, which JSON can't represent, to NULL.
func finiteOrNil(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

// lastCursor returns the (updated_at, id) of the batch's final row, which is
// where the next poll resumes once this batch has loaded.
func lastCursor(batch Batch) (Cursor, error) {
	if len(batch.Rows) == 0 {
		return Cursor{}, fmt.Errorf("empty batch has no cursor")
	}
	updatedAtIndex := slices.Index(batch.Columns, columnUpdatedAt)
	idIndex := slices.Index(batch.Columns, columnID)
	if updatedAtIndex < 0 || idIndex < 0 {
		return Cursor{}, fmt.Errorf("batch has no %s or %s column", columnUpdatedAt, columnID)
	}

	last := batch.Rows[len(batch.Rows)-1]
	updatedAt, ok := last[updatedAtIndex].(time.Time)
	if !ok {
		return Cursor{}, fmt.Errorf("%s is %T, want a timestamp", columnUpdatedAt, last[updatedAtIndex])
	}
	id, err := toInt64(last[idIndex])
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{UpdatedAt: updatedAt, ID: id}, nil
}

func toInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int16:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%s is %T, want an integer", columnID, value)
	}
}
