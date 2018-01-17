package myproxy

import (
	"log"

	_ "github.com/go-sql-driver/mysql"
	mysql "github.com/siddontang/go-mysql/mysql"
	"github.com/xwb1989/sqlparser"
)

func (h MysqlHandler) handleSelect(selectStatement *sqlparser.Select) (*mysql.Result, error) {

	newSelect := sqlparser.String(selectStatement)
	if h.verbose {
		log.Println(selectStatement, "->", newSelect)
	}
	result, err := h.SelectDB(newSelect)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return result, nil

}

// SelectDB is responsable for doing select query from Mysql
func (h MysqlHandler) SelectDB(selectStatement string) (*mysql.Result, error) {
	// 1. Exec mysql query
	rows, err := h.db.Query(selectStatement)
	if err != nil {
		return nil, err
	}

	// 2. Process result
	columns, _ := rows.Columns()
	scanArgs := make([]interface{}, len(columns))
	valueList := [][]interface{}{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		// parse records
		err = rows.Scan(scanArgs...)
		if err != nil {
			return nil, err
		}

		for i, _ := range values {
			if values[i] == nil {
				values[i] = []byte{}
			}
		}

		valueList = append(valueList, values)

	}
	result, err := mysql.BuildSimpleResultset(
		columns,
		valueList,
		false,
	)
	if err != nil {
		return nil, err
	}

	return &mysql.Result{0, 0, 0, result}, nil
}
