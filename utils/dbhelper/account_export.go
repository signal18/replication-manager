package dbhelper

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// errShowCreateUserNoMatch is ER_NO_SUCH_USER (1133): "Can't find any matching row in the user table".
const errShowCreateUserNoMatch = 1133

// ConfirmAccountHiddenBySkipNameResolve reports whether an account that a dump failed to export
// is, on the live server and under the credentials of db (the dump's own), an account the server
// ignores because of skip_name_resolve (#2436). Three live observations must all hold, an
// error 1133 alone does not prove it:
//   - skip_name_resolve is ON now;
//   - the account is in the privilege catalogue (mysql.user), so it is not a dropped one;
//   - SHOW CREATE USER on it answers error 1133 now.
//
// why says which observation failed when hidden is false. PostgreSQL has no such account
// notion (T23): it answers false without querying. The account name must not need escaping
// (a quote or a backslash in it is refused, never guessed).
func ConfirmAccountHiddenBySkipNameResolve(db *sqlx.DB, myver *version.Version, user, host string) (hidden bool, why string, err error) {
	if myver != nil && myver.IsPostgreSQL() {
		return false, "PostgreSQL has no accounts hidden by skip_name_resolve", nil
	}
	if db == nil {
		return false, "", errors.New("no connection to confirm the account on")
	}
	if !plainAccountName(user) || !plainAccountName(host) {
		return false, "", fmt.Errorf("account %q@%q has a name that needs escaping", user, host)
	}

	var skip string
	if err := db.QueryRow("SELECT @@skip_name_resolve").Scan(&skip); err != nil {
		return false, "", fmt.Errorf("read skip_name_resolve: %w", err)
	}
	if v := strings.ToUpper(strings.TrimSpace(skip)); v != "1" && v != "ON" {
		return false, "skip_name_resolve is OFF on the server", nil
	}

	var present int
	if err := db.QueryRow("SELECT COUNT(*) FROM mysql.user WHERE user = ? AND host = ?", user, host).Scan(&present); err != nil {
		return false, "", fmt.Errorf("look for the account in the privilege catalogue: %w", err)
	}
	if present == 0 {
		return false, "the account is not in the privilege catalogue", nil
	}

	// the server may answer the column header before the error: the statement is read to its end
	rows, err := db.Query("SHOW CREATE USER '" + user + "'@'" + host + "'")
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err == nil {
			return false, "SHOW CREATE USER answers: the server can show the account", nil
		}
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == errShowCreateUserNoMatch {
		return true, "", nil
	}
	return false, "", fmt.Errorf("SHOW CREATE USER: %w", err)
}

// plainAccountName tells a user or host part that can sit between single quotes as it is.
func plainAccountName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c == '\'' || c == '\\' || c == '`' || c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
