// Package migrate imports Sendy data without executing SQL from a dump.
package migrate

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-sql-driver/mysql"
)

type Row struct {
	Table  string             `json:"table"`
	Values map[string]*string `json:"values"`
}
type Source interface {
	Scan(context.Context, func(Row) error) error
}
type DumpSource struct{ Reader io.Reader }
type MySQLSource struct{ DSN string }

// Scan establishes a consistent read-only source transaction. Use a SELECT-only
// MySQL account as a second layer of protection.
func (s MySQLSource) Scan(ctx context.Context, emit func(Row) error) error {
	cfg, err := mysql.ParseDSN(s.DSN)
	if err != nil {
		return fmt.Errorf("invalid Sendy DSN: %w", err)
	}
	cfg.MultiStatements = false
	cfg.ParseTime = false
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("begin read-only Sendy snapshot: %w", err)
	}
	defer tx.Rollback()
	tables, err := tx.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return err
	}
	var names []string
	for tables.Next() {
		var n string
		if err = tables.Scan(&n); err != nil {
			tables.Close()
			return err
		}
		names = append(names, n)
	}
	err = tables.Err()
	tables.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if !safeIdentifier(name) {
			return fmt.Errorf("unsupported source table identifier %q", name)
		}
		rows, err := tx.QueryContext(ctx, "SELECT * FROM `"+name+"`")
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			args := make([]any, len(cols))
			for i := range vals {
				args[i] = &vals[i]
			}
			if err = rows.Scan(args...); err != nil {
				rows.Close()
				return err
			}
			r := Row{Table: strings.ToLower(name), Values: map[string]*string{}}
			for i, c := range cols {
				if vals[i].Valid {
					v := vals[i].String
					r.Values[strings.ToLower(c)] = &v
				} else {
					r.Values[strings.ToLower(c)] = nil
				}
			}
			if err = emit(r); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func safeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
			return false
		}
	}
	return true
}

type token struct {
	value  string
	quoted bool
}
type lexer struct {
	r       *bufio.Reader
	pending *token
}

func (l *lexer) next() (token, error) {
	if l.pending != nil {
		t := *l.pending
		l.pending = nil
		return t, nil
	}
	for {
		b, err := l.r.ReadByte()
		if err != nil {
			return token{}, err
		}
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b == '#' {
			for {
				c, e := l.r.ReadByte()
				if e != nil {
					return token{}, e
				}
				if c == '\n' {
					break
				}
			}
			continue
		}
		if b == '-' || b == '/' {
			p, _ := l.r.Peek(1)
			if len(p) > 0 && ((b == '-' && p[0] == '-') || (b == '/' && p[0] == '*')) {
				l.r.ReadByte()
				if b == '-' {
					for {
						c, e := l.r.ReadByte()
						if e != nil {
							return token{}, e
						}
						if c == '\n' {
							break
						}
					}
				} else {
					last := byte(0)
					var comment strings.Builder
					for {
						c, e := l.r.ReadByte()
						if e != nil {
							return token{}, fmt.Errorf("unterminated SQL comment")
						}
						if last == '*' && c == '/' {
							break
						}
						last = c
						// Only inspect the prefix of executable comments. Ordinary
						// mysqldump SET comments do not contain source rows.
						if comment.Len() < 4096 {
							comment.WriteByte(c)
						}
					}
					body := comment.String()
					if strings.HasPrefix(body, "!") {
						body = strings.TrimLeft(body[1:], "0123456789 \r\n\t")
						upper := strings.ToUpper(body)
						if strings.HasPrefix(upper, "INSERT") || strings.HasPrefix(upper, "REPLACE") || strings.HasPrefix(upper, "LOAD") {
							return token{}, errors.New("data statements inside executable SQL comments are unsupported; export ordinary INSERT VALUES")
						}
						if strings.Contains(upper, "NO_BACKSLASH_ESCAPES") {
							return token{}, errors.New("NO_BACKSLASH_ESCAPES dumps are unsupported; export with standard MySQL string escaping")
						}
					}
				}
				continue
			}
		}
		if b == '\'' || b == '"' || b == '`' {
			q := b
			var out strings.Builder
			for {
				c, e := l.r.ReadByte()
				if e != nil {
					return token{}, errors.New("unterminated SQL quoted value")
				}
				if c == q {
					p, _ := l.r.Peek(1)
					if len(p) > 0 && p[0] == q {
						l.r.ReadByte()
						out.WriteByte(q)
						continue
					}
					return token{out.String(), true}, nil
				}
				if c == '\\' && q != '`' {
					c, e = l.r.ReadByte()
					if e != nil {
						return token{}, e
					}
					switch c {
					case '0':
						c = 0
					case 'n':
						c = '\n'
					case 'r':
						c = '\r'
					case 't':
						c = '\t'
					case 'b':
						c = '\b'
					case 'Z':
						c = 26
					case '%', '_':
						out.WriteByte('\\')
					}
					out.WriteByte(c)
				} else {
					out.WriteByte(c)
				}
			}
		}
		if strings.ContainsRune("(),;.=", rune(b)) {
			return token{value: string(b)}, nil
		}
		var out strings.Builder
		out.WriteByte(b)
		for {
			p, e := l.r.Peek(1)
			if e != nil {
				if errors.Is(e, io.EOF) {
					return token{value: out.String()}, nil
				}
				return token{}, e
			}
			c := p[0]
			if c == ' ' || c == '\n' || c == '\r' || c == '\t' || strings.ContainsRune("(),;=\"'`", rune(c)) {
				return token{value: out.String()}, nil
			}
			l.r.ReadByte()
			out.WriteByte(c)
		}
	}
}
func (l *lexer) put(t token) { l.pending = &t }
func (l *lexer) expect(s string) error {
	t, e := l.next()
	if e != nil {
		return e
	}
	if !strings.EqualFold(t.value, s) || t.quoted {
		return fmt.Errorf("expected SQL %s, got %q", s, t.value)
	}
	return nil
}
func (l *lexer) skip() error {
	for {
		t, e := l.next()
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return e
		}
		if !t.quoted && t.value == ";" {
			return nil
		}
	}
}
func tableName(l *lexer) (string, error) {
	t, e := l.next()
	if e != nil {
		return "", e
	}
	n := t.value
	if parts := strings.Split(n, "."); len(parts) > 1 {
		n = parts[len(parts)-1]
	}
	p, e := l.next()
	if e != nil {
		return "", e
	}
	if p.value == "." && !p.quoted {
		t, e = l.next()
		if e != nil {
			return "", e
		}
		n = t.value
	} else {
		l.put(p)
	}
	return strings.ToLower(n), nil
}

// Scan parses CREATE column order and INSERT VALUES one tuple at a time. SQL
// identifiers and values become data only. No dump statement reaches SQLite.
func (s DumpSource) Scan(ctx context.Context, emit func(Row) error) error {
	l := &lexer{r: bufio.NewReaderSize(s.Reader, 64*1024)}
	schemas := map[string][]string{}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		t, e := l.next()
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return e
		}
		switch strings.ToUpper(t.value) {
		case ";":
			// Ignored executable comments leave their terminator behind.
			// It is an empty statement, not a reason to skip the next one.
			continue
		case "REPLACE", "LOAD":
			return fmt.Errorf("unsupported source data statement %s; export INSERT VALUES", t.value)
		case "SET":
			for {
				v, e := l.next()
				if errors.Is(e, io.EOF) {
					return nil
				}
				if e != nil {
					return e
				}
				if strings.Contains(strings.ToUpper(v.value), "NO_BACKSLASH_ESCAPES") {
					return errors.New("NO_BACKSLASH_ESCAPES dumps are unsupported; export with standard MySQL string escaping")
				}
				if !v.quoted && v.value == ";" {
					break
				}
			}
		case "CREATE":
			k, e := l.next()
			if e != nil {
				return e
			}
			if !strings.EqualFold(k.value, "TABLE") {
				if e = l.skip(); e != nil {
					return e
				}
				continue
			}
			k, e = l.next()
			if e != nil {
				return e
			}
			if strings.EqualFold(k.value, "IF") {
				if e = l.expect("NOT"); e != nil {
					return e
				}
				if e = l.expect("EXISTS"); e != nil {
					return e
				}
			} else {
				l.put(k)
			}
			name, e := tableName(l)
			if e != nil {
				return e
			}
			if e = l.expect("("); e != nil {
				return e
			}
			depth := 1
			first := true
			var cols []string
			for depth > 0 {
				v, e := l.next()
				if e != nil {
					return e
				}
				if first && depth == 1 {
					u := strings.ToUpper(v.value)
					if v.quoted || (u != "PRIMARY" && u != "UNIQUE" && u != "KEY" && u != "CONSTRAINT" && u != "INDEX" && u != "CHECK" && u != "FOREIGN") {
						cols = append(cols, strings.ToLower(v.value))
					}
					first = false
				}
				if !v.quoted {
					switch v.value {
					case "(":
						depth++
					case ")":
						depth--
					case ",":
						if depth == 1 {
							first = true
						}
					}
				}
			}
			schemas[name] = cols
			if e = l.skip(); e != nil {
				return e
			}
		case "INSERT":
			k, e := l.next()
			if e != nil {
				return e
			}
			if strings.EqualFold(k.value, "IGNORE") {
				k, e = l.next()
				if e != nil {
					return e
				}
			}
			if !strings.EqualFold(k.value, "INTO") {
				return errors.New("only INSERT INTO ... VALUES is supported")
			}
			name, e := tableName(l)
			if e != nil {
				return e
			}
			cols := schemas[name]
			k, e = l.next()
			if e != nil {
				return e
			}
			if k.value == "(" && !k.quoted {
				cols = nil
				for {
					c, e := l.next()
					if e != nil {
						return e
					}
					cols = append(cols, strings.ToLower(c.value))
					sep, e := l.next()
					if e != nil {
						return e
					}
					if sep.value == ")" {
						break
					}
					if sep.value != "," {
						return errors.New("invalid INSERT column list")
					}
				}
				k, e = l.next()
				if e != nil {
					return e
				}
			}
			if !strings.EqualFold(k.value, "VALUES") {
				return fmt.Errorf("unsupported INSERT expression for %s", name)
			}
			if len(cols) == 0 {
				return fmt.Errorf("INSERT for %s has no column list or preceding CREATE TABLE", name)
			}
			for {
				if e = l.expect("("); e != nil {
					return e
				}
				r := Row{Table: name, Values: map[string]*string{}}
				for i, c := range cols {
					v, e := l.next()
					if e != nil {
						return e
					}
					var value *string
					if v.quoted {
						value = &v.value
					} else if strings.EqualFold(v.value, "NULL") {
					} else if strings.HasPrefix(v.value, "0x") {
						b, e := hex.DecodeString(v.value[2:])
						if e != nil {
							return e
						}
						z := string(b)
						value = &z
					} else {
						if _, e = strconv.ParseFloat(v.value, 64); e != nil {
							return fmt.Errorf("unsupported SQL value %q in %s", v.value, name)
						}
						value = &v.value
					}
					r.Values[c] = value
					sep := ","
					if i == len(cols)-1 {
						sep = ")"
					}
					if e = l.expect(sep); e != nil {
						return e
					}
				}
				if e = emit(r); e != nil {
					return e
				}
				if e = ctx.Err(); e != nil {
					return e
				}
				k, e = l.next()
				if e != nil {
					return e
				}
				if k.value == ";" && !k.quoted {
					break
				}
				if k.value != "," || k.quoted {
					return fmt.Errorf("unsupported INSERT suffix %q", k.value)
				}
			}
		default:
			if e = l.skip(); e != nil {
				return e
			}
		}
	}
}
