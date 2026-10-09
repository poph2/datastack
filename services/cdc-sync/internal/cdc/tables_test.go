package cdc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTables(t *testing.T) {
	refs, err := ParseTables(" orders.orders, marketing.campaigns ,,")
	require.NoError(t, err)
	assert.Equal(t, []TableRef{{"orders", "orders"}, {"marketing", "campaigns"}}, refs)
	assert.Equal(t, "marketing.campaigns", refs[1].String())
}

func TestParseTablesRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no database":      "orders",
		"quote injection":  "orders.orders`; DROP",
		"three parts":      "a.b.c",
		"same target name": "a.orders,b.orders",
	}
	for name, list := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTables(list)
			assert.Error(t, err)
		})
	}
}
