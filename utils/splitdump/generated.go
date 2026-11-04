package splitdump

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	gzip "github.com/klauspost/pgzip"
)

// A generated column is computed by the server, which refuses a value for it (MySQL 3105:
// "The value specified for generated column ... is not allowed"). MySQL's mysqldump leaves
// them out of its INSERT column list; a MariaDB client dumping a MySQL server writes their
// values (INSERT INTO `t` VALUES with every column), and so does any dump without a column
// list. The restore loads such rows with an explicit column list without the generated
// columns: the server computes them, the rows are the source's.

var generatedColumnDefinition = regexp.MustCompile(`(?i)\sGENERATED\s+ALWAYS\s+AS\b`)

// GeneratedColumns reads a CREATE TABLE statement (a table's -schema file) and returns its
// columns in their order and, for each, whether it is generated (GENERATED ALWAYS AS, the
// form MySQL and MariaDB both print). Index, key and constraint lines are not columns.
func GeneratedColumns(createTable string) (columns []string, generated []bool) {
	inTable := false
	for _, line := range strings.Split(createTable, "\n") {
		t := strings.TrimSpace(line)
		if !inTable {
			inTable = strings.HasPrefix(strings.ToUpper(t), "CREATE TABLE ")
			continue
		}
		if strings.HasPrefix(t, ")") {
			break
		}
		name, rest, ok := backtickName(t)
		if !ok {
			continue
		}
		columns = append(columns, name)
		generated = append(generated, generatedColumnDefinition.MatchString(" "+rest))
	}
	return columns, generated
}

// backtickName splits a column definition line into its backtick-quoted name (“ an escaped
// backtick) and the rest; ok false when the line does not start with one.
func backtickName(line string) (name, rest string, ok bool) {
	if !strings.HasPrefix(line, "`") {
		return "", "", false
	}
	var b strings.Builder
	for i := 1; i < len(line); i++ {
		if line[i] != '`' {
			b.WriteByte(line[i])
			continue
		}
		if i+1 < len(line) && line[i+1] == '`' {
			b.WriteByte('`')
			i++
			continue
		}
		return b.String(), line[i+1:], true
	}
	return "", "", false
}

// TableGeneratedColumns returns GeneratedColumns of the -schema file of the table a data
// file holds (same directory, gzip or plain); ok false when the table has no generated
// column or its schema file is not in the backup.
func TableGeneratedColumns(dataPath string) (columns []string, generated []bool, ok bool, err error) {
	schema, table := SchemaFromFilename(dataPath), TableFromFilename(dataPath)
	if schema == "" || table == "" {
		return nil, nil, false, nil
	}
	dir := filepath.Dir(dataPath)
	for _, name := range []string{schema + "." + table + "-schema.sql.gz", schema + "." + table + "-schema.sql"} {
		f, openErr := os.Open(filepath.Join(dir, name))
		if errors.Is(openErr, os.ErrNotExist) {
			continue
		}
		if openErr != nil {
			return nil, nil, false, openErr
		}
		defer f.Close()
		var r io.Reader = bufio.NewReader(f)
		if strings.HasSuffix(name, ".gz") {
			gz, gzErr := gzip.NewReader(r)
			if gzErr != nil {
				return nil, nil, false, gzErr
			}
			defer gz.Close()
			r = gz
		}
		// a CREATE TABLE is small; the bound keeps a corrupt file from growing memory (T18)
		data, readErr := io.ReadAll(io.LimitReader(r, 16<<20))
		if readErr != nil {
			return nil, nil, false, readErr
		}
		columns, generated = GeneratedColumns(string(data))
		for _, g := range generated {
			if g {
				return columns, generated, true, nil
			}
		}
		return nil, nil, false, nil
	}
	return nil, nil, false, nil
}

var insertWithoutColumnList = regexp.MustCompile("(?is)^((?:INSERT(?:\\s+IGNORE)?|REPLACE)\\s+INTO\\s+`(?:[^`]|``)+`(?:\\.`(?:[^`]|``)+`)?)\\s+VALUES\\s*")

// InsertWithoutGeneratedColumns rewrites an INSERT [IGNORE] / REPLACE INTO `t` VALUES
// (...),(...) statement without a column list into the same rows with an explicit column
// list that leaves the generated columns out, their values dropped from each row. A
// statement with a column list is returned as it is. An error when a row does not parse or
// does not carry one value per column: the caller then sends the statement unchanged, and
// the server's own error surfaces.
func InsertWithoutGeneratedColumns(stmt string, columns []string, generated []bool) (string, error) {
	m := insertWithoutColumnList.FindStringSubmatchIndex(stmt)
	if m == nil {
		return stmt, nil
	}
	rows, err := splitValueRows(stmt[m[1]:])
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(stmt[m[2]:m[3]])
	b.WriteString(" (")
	first := true
	for i, c := range columns {
		if generated[i] {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString("`" + strings.ReplaceAll(c, "`", "``") + "`")
	}
	b.WriteString(") VALUES ")
	for r, values := range rows {
		if len(values) != len(columns) {
			return "", errors.New("splitdump: a row does not carry one value per column of the table")
		}
		if r > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		first = true
		for i, v := range values {
			if generated[i] {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(v)
		}
		b.WriteByte(')')
	}
	return b.String(), nil
}

// splitValueRows splits the rows after VALUES, "(v1,v2),(v1,v2)", into their values as
// written: quoted strings ('...' or "...", backslash escapes, a doubled quote) and nested
// parentheses are kept whole. Anything else than rows separated by commas is an error.
func splitValueRows(s string) ([][]string, error) {
	var rows [][]string
	i, n := 0, len(s)
	skipSpace := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
			i++
		}
	}
rowLoop:
	for {
		skipSpace()
		if i >= n || s[i] != '(' {
			return nil, errors.New("splitdump: a row of the INSERT does not start with (")
		}
		i++
		var values []string
		start, depth := i, 0
		for {
			if i >= n {
				return nil, errors.New("splitdump: a row of the INSERT is not closed")
			}
			c := s[i]
			switch {
			case c == '\'' || c == '"':
				i++
				for i < n {
					if s[i] == '\\' {
						i += 2
						continue
					}
					if s[i] == c {
						break
					}
					i++
				}
				if i >= n {
					return nil, errors.New("splitdump: a string of the INSERT is not closed")
				}
				i++
				continue
			case c == '(':
				depth++
			case c == ')' && depth > 0:
				depth--
			case c == ',' && depth == 0:
				values = append(values, strings.TrimSpace(s[start:i]))
				start = i + 1
			case c == ')':
				values = append(values, strings.TrimSpace(s[start:i]))
				rows = append(rows, values)
				i++
				skipSpace()
				if i >= n {
					return rows, nil
				}
				if s[i] != ',' {
					return nil, errors.New("splitdump: unexpected text after a row of the INSERT")
				}
				i++
				continue rowLoop
			}
			i++
		}
	}
}
