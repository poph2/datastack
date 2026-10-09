package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractSQL(t *testing.T) {
	tests := map[string]struct {
		reply string
		want  string
	}{
		"bare":            {reply: "SELECT 1", want: "SELECT 1"},
		"trailing ;":      {reply: "SELECT 1;", want: "SELECT 1"},
		"sql fence":       {reply: "```sql\nSELECT country FROM customers;\n```", want: "SELECT country FROM customers"},
		"plain fence":     {reply: "```\nSELECT 1\n```", want: "SELECT 1"},
		"fence and prose": {reply: "Here is the query:\n```sql\nSELECT 1\n```\nIt counts rows.", want: "SELECT 1"},
		"prose before":    {reply: "To select the data, run:\nSELECT 2 FROM t", want: "SELECT 2 FROM t"},
		"same-line prose": {reply: "Query: SELECT 3", want: "SELECT 3"},
		"first statement": {reply: "SELECT 1; SELECT 2;", want: "SELECT 1"},
		"; in a literal":  {reply: "SELECT 'a;b' FROM t; DROP TABLE t", want: "SELECT 'a;b' FROM t"},
		"with":            {reply: "WITH x AS (SELECT 1) SELECT * FROM x", want: "WITH x AS (SELECT 1) SELECT * FROM x"},
		"think block":     {reply: "<think>I should SELECT revenue first.</think>\nSELECT 4", want: "SELECT 4"},
		"unclosed fence":  {reply: "```sql\nSELECT 5", want: "SELECT 5"},
		"multiline":       {reply: "SELECT a,\n  b\nFROM t\nWHERE c = 1", want: "SELECT a,\n  b\nFROM t\nWHERE c = 1"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ExtractSQL(tt.reply)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractSQLWithoutQuery(t *testing.T) {
	for _, reply := range []string{"", "I don't know.", "```\n```", "<think>SELECT 1</think>no idea"} {
		_, err := ExtractSQL(reply)
		assert.ErrorIs(t, err, ErrNoSQL, reply)
	}
}
