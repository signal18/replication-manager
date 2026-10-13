// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"fmt"
	"strings"

	"github.com/go-mysql-org/go-mysql/schema"
)

// How the plugin finds a row on the target and writes its values (#2015): rows are found by
// the source's primary key (the ColumnStore copy has no key: #2010), ENUM and SET written as
// their text. Pure, so tested without a database.

// filterable: ColumnStore refuses a BLOB or TEXT column in a WHERE ("VARBINARY/BLOB in
// filter or function is not supported"), JSON and BINARY included, converted to them (#2010)
func filterable(c *schema.TableColumn) bool {
	switch c.Type {
	case schema.TYPE_STRING:
		t := strings.ToLower(c.RawType)
		return !strings.Contains(t, "blob") && !strings.Contains(t, "text") && !strings.Contains(t, "binary")
	case schema.TYPE_JSON, schema.TYPE_BINARY:
		return false
	}
	return true
}

// keyColumns are the columns a row is found by on the target: the source's primary key,
// else every filterable column. An error when the key holds a column ColumnStore cannot
// filter on (a BINARY primary key became a BLOB): such a table's updates and deletes are
// skipped.
func keyColumns(t *schema.Table) ([]int, error) {
	if len(t.PKColumns) > 0 {
		for _, i := range t.PKColumns {
			if !filterable(&t.Columns[i]) {
				return nil, fmt.Errorf("%s.%s: its primary key %s is a BLOB on ColumnStore, which cannot filter on it", t.Schema, t.Name, t.Columns[i].Name)
			}
		}
		return t.PKColumns, nil
	}
	var cols []int
	for i := range t.Columns {
		if filterable(&t.Columns[i]) {
			cols = append(cols, i)
		}
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%s.%s: no primary key and no column ColumnStore can filter on", t.Schema, t.Name)
	}
	return cols, nil
}

// value converts a row value to what the target column takes: an ENUM arrives as its index
// and a SET as its bitmask, the ColumnStore copy holds their text (VARCHAR, #2010).
func value(c *schema.TableColumn, v interface{}) interface{} {
	switch c.Type {
	case schema.TYPE_ENUM:
		if i, ok := toInt(v); ok {
			if i <= 0 || int(i) > len(c.EnumValues) {
				return ""
			}
			return c.EnumValues[i-1]
		}
	case schema.TYPE_SET:
		if bits, ok := toInt(v); ok {
			var set []string
			for i, s := range c.SetValues {
				if bits&(1<<uint(i)) != 0 {
					set = append(set, s)
				}
			}
			return strings.Join(set, ",")
		}
	}
	return v
}

func toInt(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int16:
		return int64(n), true
	case int8:
		return int64(n), true
	case int:
		return int64(n), true
	case uint64:
		return int64(n), true
	}
	return 0, false
}
