package analyzer

import (
	"fmt"
	"strings"
)

// forbiddenKeywords can't appear (outside string literals, quoted
// identifiers and comments) in a generated query. The guard is defence in
// depth: the real protection is the read-only StarRocks user. It is
// deliberately blunt, since a false positive only costs a repair attempt.
var forbiddenKeywords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "UPSERT": true, "MERGE": true,
	"DROP": true, "ALTER": true, "CREATE": true, "TRUNCATE": true, "RENAME": true,
	"GRANT": true, "REVOKE": true, "SET": true, "USE": true,
	"INTO": true, "OUTFILE": true, "DUMPFILE": true, "LOAD": true, "EXPORT": true, "SUBMIT": true,
	"KILL": true, "SHUTDOWN": true, "ADMIN": true, "INSTALL": true, "UNINSTALL": true,
	"CALL": true, "EXECUTE": true, "PREPARE": true, "BACKUP": true, "RESTORE": true,
	"CANCEL": true, "REFRESH": true, "LOCK": true, "UNLOCK": true,
}

// UnsafeSQLError explains why the guard rejected a query.
type UnsafeSQLError struct {
	Reason string
}

func (e *UnsafeSQLError) Error() string {
	return "unsafe SQL: " + e.Reason
}

// CheckSQL accepts exactly one SELECT (or WITH … SELECT) statement with no
// write or DDL keyword, and returns it without trailing semicolons.
func CheckSQL(sql string) (string, error) {
	statement := strings.TrimRight(strings.TrimSpace(sql), "; \t\r\n")
	if statement == "" {
		return "", &UnsafeSQLError{Reason: "empty statement"}
	}
	// /*! … */ is executed by MySQL-compatible servers, and /*+ … */ carries
	// hints such as SET_VAR that could lift the query timeout.
	if strings.Contains(statement, "/*!") || strings.Contains(statement, "/*+") {
		return "", &UnsafeSQLError{Reason: "executable comments and optimizer hints are not allowed"}
	}

	words, semicolon := sqlWords(statement)
	if semicolon {
		return "", &UnsafeSQLError{Reason: "only one statement is allowed"}
	}
	if len(words) == 0 || (words[0] != "SELECT" && words[0] != "WITH") {
		return "", &UnsafeSQLError{Reason: "the statement must start with SELECT or WITH"}
	}
	for _, word := range words {
		if forbiddenKeywords[word] {
			return "", &UnsafeSQLError{Reason: fmt.Sprintf("keyword %s is not allowed", word)}
		}
	}
	return statement, nil
}

// sqlWords returns the upper-cased words of statement outside literals and
// comments, and whether it contains a semicolon there.
func sqlWords(statement string) (words []string, semicolon bool) {
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			words = append(words, strings.ToUpper(current.String()))
			current.Reset()
		}
	}

	last := -1
	scanSQL(statement, func(i int, ch byte) bool {
		// A skipped literal or comment between two bytes ends a word.
		if i != last+1 {
			flush()
		}
		last = i
		switch {
		case isWordByte(ch):
			current.WriteByte(ch)
		case ch == ';':
			semicolon = true
			flush()
		default:
			flush()
		}
		return true
	})
	flush()
	return words, semicolon
}

func isWordByte(ch byte) bool {
	return ch == '_' || ch == '$' ||
		(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}
