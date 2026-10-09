package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSQLAcceptsReadOnlyQueries(t *testing.T) {
	tests := map[string]struct {
		sql  string
		want string
	}{
		"select":                {sql: "SELECT 1", want: "SELECT 1"},
		"lower case":            {sql: "select count(*) from orders", want: "select count(*) from orders"},
		"trailing semicolons":   {sql: "SELECT 1;;  \n", want: "SELECT 1"},
		"with":                  {sql: "WITH t AS (SELECT 1 AS x) SELECT x FROM t", want: "WITH t AS (SELECT 1 AS x) SELECT x FROM t"},
		"keyword in string":     {sql: "SELECT * FROM orders WHERE failure_reason = 'DROP TABLE; DELETE'", want: "SELECT * FROM orders WHERE failure_reason = 'DROP TABLE; DELETE'"},
		"keyword in identifier": {sql: "SELECT `update` FROM t", want: "SELECT `update` FROM t"},
		"keyword in comment":    {sql: "SELECT 1 -- not an INSERT\n", want: "SELECT 1 -- not an INSERT"},
		"block comment":         {sql: "SELECT /* delete me */ 1", want: "SELECT /* delete me */ 1"},
		"keyword inside word":   {sql: "SELECT updated_at, created_at, deleted_at, offset_x FROM orders", want: "SELECT updated_at, created_at, deleted_at, offset_x FROM orders"},
		"escaped quote":         {sql: `SELECT 'it''s', 'a\'b' FROM t`, want: `SELECT 'it''s', 'a\'b' FROM t`},
		"leading whitespace":    {sql: "\n  SELECT 1", want: "SELECT 1"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := CheckSQL(tt.sql)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCheckSQLRejectsUnsafeQueries(t *testing.T) {
	tests := map[string]struct {
		sql    string
		reason string
	}{
		"empty":                 {sql: " ; ", reason: "empty statement"},
		"insert":                {sql: "INSERT INTO orders VALUES (1)", reason: "must start with SELECT or WITH"},
		"show":                  {sql: "SHOW GRANTS", reason: "must start with SELECT or WITH"},
		"two statements":        {sql: "SELECT 1; DROP TABLE orders", reason: "only one statement"},
		"two selects":           {sql: "SELECT 1; SELECT 2", reason: "only one statement"},
		"cte with delete":       {sql: "WITH x AS (DELETE FROM orders) SELECT 1", reason: "keyword DELETE"},
		"select into outfile":   {sql: "SELECT * FROM orders INTO OUTFILE 's3://bucket/x'", reason: "keyword INTO"},
		"set in select":         {sql: "SELECT 1 FROM t WHERE x = 1 SET y = 2", reason: "keyword SET"},
		"lower case keyword":    {sql: "select 1 from t where 1 = (drop)", reason: "keyword DROP"},
		"executable comment":    {sql: "SELECT 1 /*!50000 DROP TABLE orders */", reason: "executable comments"},
		"optimizer hint":        {sql: "SELECT /*+ SET_VAR(query_timeout = 3600) */ 1", reason: "optimizer hints"},
		"keyword after literal": {sql: "SELECT 'a'DROP", reason: "keyword DROP"},
		"comment hides nothing": {sql: "SELECT 1 /* x */; DROP TABLE t", reason: "only one statement"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := CheckSQL(tt.sql)
			var unsafe *UnsafeSQLError
			require.ErrorAs(t, err, &unsafe)
			assert.Contains(t, unsafe.Reason, tt.reason)
		})
	}
}
