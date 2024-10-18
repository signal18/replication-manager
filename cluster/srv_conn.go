package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// All next query will not use binlog, except changing database via USE
func (server *ServerMonitor) GetConnNoBinlog(db *sqlx.DB) (*sqlx.Conn, error) {
	if db == nil {
		return nil, nil
	}

	conn, err := db.Connx(context.Background())
	if err != nil {
		return nil, fmt.Errorf("Error getting single connection, %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = conn.ExecContext(ctx, "set sql_log_bin=0")
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("Error disabling binlog, %s", err)
	}

	return conn, nil
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnGetQuery(conn *sqlx.Conn, dest interface{}, query string) error {
	cluster := server.ClusterGroup
	if conn == nil {
		return errors.New("No connection established")
	}

	return server.ConnGetQueryWithTimeout(conn, dest, query, time.Duration(cluster.Conf.ReadTimeout))
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnGetQueryWithTimeout(conn *sqlx.Conn, dest interface{}, query string, timeout time.Duration) error {
	if conn == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := conn.GetContext(ctx, dest, query)
	if err != nil {
		return fmt.Errorf("Error disabling binlog, %s", err)
	}

	return nil
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnExecQuery(conn *sqlx.Conn, query string) (sql.Result, error) {
	cluster := server.ClusterGroup
	if conn == nil {
		return nil, errors.New("No connection established")
	}

	return server.ConnExecQueryWithTimeout(conn, query, time.Duration(cluster.Conf.ReadTimeout))
}

// This function will execute query and will use parameter for timeout
func (server *ServerMonitor) ConnExecQueryWithTimeout(conn *sqlx.Conn, query string, timeout time.Duration) (sql.Result, error) {
	if conn == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	res, err := conn.ExecContext(ctx, query)
	if err != nil {
		return res, fmt.Errorf("Error disabling binlog, %s", err)
	}

	return res, nil
}
