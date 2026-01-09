// This file contains MySQL-specific utility functions.
// It provides MySQL-only operations such as errant transaction detection
// using GTID subset comparison that are not available in MariaDB or PostgreSQL.

package dbhelper

import (
	"github.com/jmoiron/sqlx"
)

func HaveErrantTransactions(db *sqlx.DB, gtidMaster string, gtidSlave string) (bool, string, error) {

	count := 0
	query := "SELECT gtid_subset(?, ?) AS slave_is_subset"

	err := db.QueryRowx(query, gtidSlave, gtidMaster).Scan(&count)
	if err != nil {
		return false, query, err
	}

	if count == 0 {
		return true, query, nil
	}
	return false, query, nil
}
