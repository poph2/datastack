package cdc

import (
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeRows(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)
	batch := Batch{
		Columns:  []string{"id", "price", "updated_at", "deleted_at", "active", "ship_date", "ratio"},
		TypeOIDs: []uint32{pgtype.Int8OID, pgtype.NumericOID, pgtype.TimestamptzOID, pgtype.TimestamptzOID, pgtype.BoolOID, pgtype.DateOID, pgtype.Float8OID},
		Rows: [][]any{{
			int64(7),
			pgtype.Numeric{Int: big.NewInt(1999), Exp: -2, Valid: true},
			time.Date(2026, 10, 8, 14, 5, 1, 123456000, berlin),
			nil,
			true,
			time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
			math.NaN(),
		}},
	}

	body, err := encodeRows(batch)

	require.NoError(t, err)
	assert.JSONEq(t, `[{
		"id": 7,
		"price": "19.99",
		"updated_at": "2026-10-08 12:05:01.123456",
		"deleted_at": null,
		"active": true,
		"ship_date": "2026-10-09",
		"ratio": null
	}]`, string(body))
}

func TestEncodeNumericKeepsExactDigits(t *testing.T) {
	huge, _ := new(big.Int).SetString("12345678901234567890123", 10)

	assert.Equal(t, "123456789012345678901.23", encodeValue(pgtype.Numeric{Int: huge, Exp: -2, Valid: true}, pgtype.NumericOID))
	assert.Equal(t, "50.00", encodeValue(pgtype.Numeric{Int: big.NewInt(5000), Exp: -2, Valid: true}, pgtype.NumericOID))
	assert.Nil(t, encodeValue(pgtype.Numeric{NaN: true, Valid: true}, pgtype.NumericOID))
}

func TestLastCursor(t *testing.T) {
	ts := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	batch := Batch{
		Columns: []string{"id", "updated_at"},
		Rows:    [][]any{{int64(1), ts.Add(-time.Second)}, {int64(9), ts}},
	}

	cursor, err := lastCursor(batch)

	require.NoError(t, err)
	assert.Equal(t, Cursor{UpdatedAt: ts, ID: 9}, cursor)
}
