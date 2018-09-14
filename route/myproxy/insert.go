package myproxy

import (
	_ "github.com/go-sql-driver/mysql"
	siddonmysql "github.com/siddontang/go-mysql/mysql"
	"github.com/xwb1989/sqlparser"
)

func (h MysqlHandler) handleInsert(insert *sqlparser.Insert) (*siddonmysql.Result, error) {
	result, err := h.insertDB(sqlparser.String(insert))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// insertDB is responsable for doing insert into Mysql
func (h MysqlHandler) insertDB(insert string) (*siddonmysql.Result, error) {
	// 1. Exec mysql insert
	dbresult, err := h.db.Exec(insert)
	if err != nil {
		return nil, err
	}

	// 2. Process result
	insertId, err := dbresult.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &siddonmysql.Result{
		InsertId: uint64(insertId),
	}, nil
}
