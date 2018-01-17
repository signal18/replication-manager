package myproxy

import (
	_ "github.com/go-sql-driver/mysql"
	siddonmysql "github.com/siddontang/go-mysql/mysql"
	"github.com/xwb1989/sqlparser"
)

func (h MysqlHandler) handleDelete(delete *sqlparser.Delete) (*siddonmysql.Result, error) {
	result, err := h.deleteDB(sqlparser.String(delete))
	if err != nil {
		return nil, err
	}
	return result, nil
}

// updateDB is responsable for doing delete Mysql
func (h MysqlHandler) deleteDB(delete string) (*siddonmysql.Result, error) {
	// 1. Exec mysql delete
	dbresult, err := h.db.Exec(delete)
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
