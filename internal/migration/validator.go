package migration

import (
	"fmt"
	"strings"
	"unicode"
)

func ValidateTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return ErrEmptyTitle
	}
	return nil
}

func ValidateSQL(sql string) error {
	stmts := splitAndTokenize(sql)
	if len(stmts) == 0 {
		return ErrEmptySQL
	}

	for _, st := range stmts {
		if reason := checkNonTransactional(st.tokens); reason != "" {
			return fmt.Errorf("%w: %s", ErrNonTransactionalSQL, reason)
		}
	}
	return nil
}

func SplitStatements(sql string) []string {
	stmts := splitAndTokenize(sql)
	out := make([]string, 0, len(stmts))
	for _, st := range stmts {
		out = append(out, strings.TrimSpace(st.raw))
	}
	return out
}

type parsedStatement struct {
	raw    string
	tokens []string
}

func checkNonTransactional(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}

	first := tokens[0]

	switch first {
	case "VACUUM":
		return "VACUUM cannot run inside a transaction block"
	case "CLUSTER":
		return "CLUSTER cannot run inside a transaction block"
	case "BEGIN", "COMMIT", "END", "ROLLBACK", "ABORT":
		return fmt.Sprintf("manual transaction control (%s) is not allowed; migrations run in a managed transaction", first)
	case "START":
		if len(tokens) >= 2 && tokens[1] == "TRANSACTION" {
			return "manual transaction control (START TRANSACTION) is not allowed; migrations run in a managed transaction"
		}
	case "ALTER":
		if len(tokens) >= 2 && tokens[1] == "SYSTEM" {
			return "ALTER SYSTEM cannot run inside a transaction block"
		}
	case "CREATE":
		if len(tokens) >= 2 {
			switch tokens[1] {
			case "DATABASE":
				return "CREATE DATABASE cannot run inside a transaction block"
			case "TABLESPACE":
				return "CREATE TABLESPACE cannot run inside a transaction block"
			case "INDEX":
				if len(tokens) >= 3 && tokens[2] == "CONCURRENTLY" {
					return "CREATE INDEX CONCURRENTLY cannot run inside a transaction block"
				}
			case "UNIQUE":
				if len(tokens) >= 4 && tokens[2] == "INDEX" && tokens[3] == "CONCURRENTLY" {
					return "CREATE UNIQUE INDEX CONCURRENTLY cannot run inside a transaction block"
				}
			}
		}
	case "DROP":
		if len(tokens) >= 2 {
			switch tokens[1] {
			case "DATABASE":
				return "DROP DATABASE cannot run inside a transaction block"
			case "TABLESPACE":
				return "DROP TABLESPACE cannot run inside a transaction block"
			case "INDEX":
				if len(tokens) >= 3 && tokens[2] == "CONCURRENTLY" {
					return "DROP INDEX CONCURRENTLY cannot run inside a transaction block"
				}
			}
		}
	case "REINDEX":
		for _, tok := range tokens[1:] {
			if tok == "CONCURRENTLY" {
				return "REINDEX CONCURRENTLY cannot run inside a transaction block"
			}
		}
	}

	return ""
}

func splitAndTokenize(sql string) []parsedStatement {
	var results []parsedStatement
	var currentTokens []string
	stmtStart := 0

	n := len(sql)
	i := 0

	flush := func(end int) {
		if len(currentTokens) > 0 {
			results = append(results, parsedStatement{
				raw:    strings.TrimSpace(sql[stmtStart:end]),
				tokens: currentTokens,
			})
		}
		currentTokens = nil
		stmtStart = end + 1
	}

	for i < n {
		ch := sql[i]

		if unicode.IsSpace(rune(ch)) {
			i++
			continue
		}

		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			continue
		}

		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			depth := 1
			i += 2
			for i < n && depth > 0 {
				if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
					depth++
					i += 2
				} else if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			continue
		}

		if ch == '\'' {
			i++
			for i < n {
				if sql[i] == '\'' {
					i++
					if i < n && sql[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			currentTokens = append(currentTokens, "<STRING>")
			continue
		}

		if ch == '"' {
			i++
			for i < n {
				if sql[i] == '"' {
					i++
					if i < n && sql[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
			currentTokens = append(currentTokens, "<IDENT>")
			continue
		}

		if ch == '$' {
			if tag, ok := matchDollarTag(sql[i:]); ok {
				i += len(tag)
				closeIdx := strings.Index(sql[i:], tag)
				if closeIdx >= 0 {
					i += closeIdx + len(tag)
				} else {
					i = n
				}
				currentTokens = append(currentTokens, "<DOLLAR_STRING>")
				continue
			}
		}

		if ch == ';' {
			flush(i)
			i++
			continue
		}

		if isWordStart(ch) {
			start := i
			i++
			for i < n && isWordPart(sql[i]) {
				i++
			}
			word := strings.ToUpper(sql[start:i])
			currentTokens = append(currentTokens, word)
			continue
		}

		start := i
		i++
		for i < n && !unicode.IsSpace(rune(sql[i])) && sql[i] != ';' && sql[i] != '\'' && sql[i] != '"' && sql[i] != '$' && sql[i] != '-' && sql[i] != '/' && !isWordStart(sql[i]) {
			i++
		}
		currentTokens = append(currentTokens, sql[start:i])
	}

	if len(currentTokens) > 0 {
		results = append(results, parsedStatement{
			raw:    strings.TrimSpace(sql[stmtStart:n]),
			tokens: currentTokens,
		})
	}

	return results
}

func matchDollarTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	if s[1] == '$' {
		return "$$", true
	}
	if !isWordStart(s[1]) {
		return "", false
	}
	for i := 2; i < len(s); i++ {
		if s[i] == '$' {
			return s[:i+1], true
		}
		if !isWordPart(s[i]) {
			return "", false
		}
	}
	return "", false
}

func isWordStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isWordPart(b byte) bool {
	return isWordStart(b) || (b >= '0' && b <= '9')
}
