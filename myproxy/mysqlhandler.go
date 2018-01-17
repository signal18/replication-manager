package myproxy

import (
	"fmt"
	"log"

	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	. "github.com/siddontang/go-mysql/mysql"
	"github.com/xwb1989/sqlparser"
)

type MysqlHandler struct {
	db *sql.DB
}

func (h MysqlHandler) UseDB(dbName string) error {
	log.Printf("use %s \n", dbName)
	_, err := h.SelectDB("use " + dbName)
	if err != nil {
		log.Printf("Error in use %s \n", err)
	}

	return nil
}

// HandleQuery is response to process select/insert/update/delete statement
func (h MysqlHandler) HandleQuery(queryStr string) (*Result, error) {
	// 1. parse
	statement, err := sqlparser.Parse(queryStr)
	if err != nil {
		log.Printf("HandleQuery: %s, err=%v \n", queryStr, err)
		return nil, err
	}

	var ok bool
	var query *sqlparser.Select
	var insert *sqlparser.Insert
	var update *sqlparser.Update
	var delete *sqlparser.Delete

	var result *Result

	// 2. convert type to Select/Insert/Upadte/Delete
	switch statement.(type) {
	case *sqlparser.Select:
		query, ok = statement.(*sqlparser.Select)
		if !ok {
			return nil, fmt.Errorf("convert to select sql failed. sql=%s \n", query)
		}
		result, err = h.handleSelect(query)

	case *sqlparser.Insert:
		insert, ok = statement.(*sqlparser.Insert)
		if !ok {
			return nil, fmt.Errorf("convert to insert sql failed. sql=%s \n", insert)
		}
		result, err = h.handleInsert(insert)

	case *sqlparser.Update:
		update, ok = statement.(*sqlparser.Update)
		if !ok {
			return nil, fmt.Errorf("convert to update sql failed. sql=%s \n", update)
		}
		result, err = h.handleUpdate(update)

	case *sqlparser.Delete:
		delete, ok = statement.(*sqlparser.Delete)
		if !ok {
			return nil, fmt.Errorf("convert to delete sql failed. sql=%s \n", delete)
		}
		result, err = h.handleDelete(delete)

	case *sqlparser.Show:
		query, ok = statement.(*sqlparser.Select)
		if !ok {
			return nil, fmt.Errorf("convert to select sql failed. sql=%s \n", query)
		}
		result, err = h.handleSelect(query)

	default:

	}

	return result, err
}

func (h MysqlHandler) HandleFieldList(table string, fieldWildcard string) ([]*Field, error) {
	return nil, fmt.Errorf("HandleFieldList: not supported now")
}

func (h MysqlHandler) HandleStmtPrepare(query string) (int, int, interface{}, error) {
	return 0, 0, nil, fmt.Errorf("HandleStmtPrepare: not supported now")
}

func (h MysqlHandler) HandleStmtExecute(context interface{}, query string, args []interface{}) (*Result, error) {
	return nil, fmt.Errorf("HandleStmtExecute: not supported now")
}

func (h MysqlHandler) HandleStmtClose(context interface{}) error {
	log.Printf("HandleStmtClose\n")
	return nil
}
