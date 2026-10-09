package analyzer

import (
	"errors"
	"regexp"
	"strings"
)

// ErrNoSQL means the model's reply contained no recognisable query.
var ErrNoSQL = errors.New("no SQL query found in the model's reply")

var (
	// thinkBlock is the visible reasoning some local models (qwen3, deepseek)
	// emit before their answer.
	thinkBlock = regexp.MustCompile(`(?is)<think>.*?</think>`)
	// codeFence captures the body of the first ``` block, with or without a
	// language tag.
	codeFence = regexp.MustCompile("(?s)```[a-zA-Z]*[ \t]*\n?(.*?)```")
	// lineQueryStart finds a query that starts its own line, which skips a
	// preamble such as "To select the data, ...". queryStart is the fallback
	// for a query that follows prose on the same line.
	lineQueryStart = regexp.MustCompile(`(?im)^[ \t]*(SELECT|WITH)\b`)
	queryStart     = regexp.MustCompile(`(?i)\b(SELECT|WITH)\b`)
)

// ExtractSQL pulls one SQL statement out of an LLM reply. Models are told to
// answer with bare SQL, but small ones still wrap it in markdown or prose, so
// this strips reasoning blocks and code fences, skips any preamble, and keeps
// only the first statement.
func ExtractSQL(reply string) (string, error) {
	text := thinkBlock.ReplaceAllString(reply, "")
	if fenced := codeFence.FindStringSubmatch(text); fenced != nil {
		text = fenced[1]
	}
	text = strings.ReplaceAll(text, "```", "")

	loc := lineQueryStart.FindStringIndex(text)
	if loc == nil {
		loc = queryStart.FindStringIndex(text)
	}
	if loc == nil {
		return "", ErrNoSQL
	}
	statement := firstStatement(text[loc[0]:])
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return "", ErrNoSQL
	}
	return statement, nil
}

// firstStatement cuts text at the first semicolon outside a string literal,
// quoted identifier or comment.
func firstStatement(text string) string {
	end := len(text)
	scanSQL(text, func(i int, ch byte) bool {
		if ch == ';' {
			end = i
			return false
		}
		return true
	})
	return text[:end]
}

// scanSQL calls visit for every byte of text that is plain SQL, skipping
// string literals ('…', "…"), quoted identifiers (`…`) and comments (-- …,
// # …, /* … */). visit returns false to stop the scan.
func scanSQL(text string, visit func(i int, ch byte) bool) {
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case ch == '\'' || ch == '"' || ch == '`':
			i = skipQuoted(text, i)
		case ch == '-' && i+1 < len(text) && text[i+1] == '-', ch == '#':
			i = skipLine(text, i)
		case ch == '/' && i+1 < len(text) && text[i+1] == '*':
			i = skipBlockComment(text, i)
		default:
			if !visit(i, ch) {
				return
			}
		}
	}
}

// skipQuoted returns the index of the closing quote of the literal opened at
// start (or the end of text). A doubled quote or a backslash escapes it.
func skipQuoted(text string, start int) int {
	quote := text[start]
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			if quote != '`' {
				i++
			}
		case quote:
			if i+1 < len(text) && text[i+1] == quote {
				i++
				continue
			}
			return i
		}
	}
	return len(text)
}

func skipLine(text string, start int) int {
	if end := strings.IndexByte(text[start:], '\n'); end >= 0 {
		return start + end
	}
	return len(text)
}

func skipBlockComment(text string, start int) int {
	if end := strings.Index(text[start+2:], "*/"); end >= 0 {
		return start + 2 + end + 1
	}
	return len(text)
}
