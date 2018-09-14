package myproxy

import (
	_ "github.com/go-sql-driver/mysql"
	siddonmysql "github.com/siddontang/go-mysql/mysql"
	"github.com/xwb1989/sqlparser"
)

func (h MysqlHandler) handleUpdate(update *sqlparser.Update) (*siddonmysql.Result, error) {
	result, err := h.updateDB(sqlparser.String(update))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// updateDB is responsable for doing update Mysql
func (h MysqlHandler) updateDB(update string) (*siddonmysql.Result, error) {
	// 1. Exec mysql update
	dbresult, err := h.db.Exec(update)
	if err != nil {
		return nil, err
	}

	// 2. Process result
	num, err := dbresult.RowsAffected()
	if err != nil {
		return nil, err
	}

	return &siddonmysql.Result{
		AffectedRows: uint64(num),
	}, nil
}
