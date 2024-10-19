package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// All next query will not use binlog, except changing database via USE
func (server *ServerMonitor) GetConnNoBinlog(db *sqlx.DB) (*sqlx.Conn, error) {
	cluster := server.ClusterGroup
	if db == nil {
		return nil, nil
	}

	conn, err := db.Connx(context.Background())
	if err != nil {
		return nil, fmt.Errorf("Error getting single connection, %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ReadTimeout)*time.Second)
	defer cancel()

	_, err = conn.ExecContext(ctx, "set sql_log_bin=0")
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("Error disabling binlog, %s", err)
	}

	return conn, nil
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnGetQuery(conn *sqlx.Conn, dest interface{}, query string, args ...interface{}) error {
	cluster := server.ClusterGroup
	if conn == nil {
		return errors.New("No connection established")
	}

	return server.ConnGetQueryWithTimeout(conn, dest, query, time.Duration(cluster.Conf.ReadTimeout)*time.Second, args...)
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnGetQueryWithTimeout(conn *sqlx.Conn, dest interface{}, query string, timeout time.Duration, args ...interface{}) error {
	if conn == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := conn.GetContext(ctx, dest, query, args...)
	if err != nil {
		return fmt.Errorf("Error disabling binlog, %s", err)
	}

	return nil
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnSelectQuery(conn *sqlx.Conn, dest interface{}, query string, args ...interface{}) error {
	cluster := server.ClusterGroup
	if conn == nil {
		return errors.New("No connection established")
	}

	return server.ConnSelectQueryWithTimeout(conn, dest, query, time.Duration(cluster.Conf.ReadTimeout)*time.Second, args...)
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnSelectQueryWithTimeout(conn *sqlx.Conn, dest interface{}, query string, timeout time.Duration, args ...interface{}) error {
	if conn == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := conn.SelectContext(ctx, dest, query, args...)
	if err != nil {
		return err
	}

	return nil
}

// This function will execute query and will use default read timeout
func (server *ServerMonitor) ConnExecQuery(conn *sqlx.Conn, query string, args ...interface{}) (sql.Result, error) {
	cluster := server.ClusterGroup
	if conn == nil {
		return nil, errors.New("No connection established")
	}

	return server.ConnExecQueryWithTimeout(conn, query, time.Duration(cluster.Conf.ReadTimeout)*time.Second, args...)
}

// This function will execute query and will use parameter for timeout
func (server *ServerMonitor) ConnExecQueryWithTimeout(conn *sqlx.Conn, query string, timeout time.Duration, args ...interface{}) (sql.Result, error) {
	if conn == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	res, err := conn.ExecContext(ctx, query, args...)
	if err != nil {
		return res, fmt.Errorf("Error disabling binlog, %s", err)
	}

	return res, nil
}

func isTableMissingError(err error) bool {
	// Customize this function to match your specific DB error code for a missing table
	return err != nil && strings.HasSuffix(err.Error(), "doesn't exist")
}

func isNoConnPoolError(err error) bool {
	// Customize this function to match your specific DB error code for a missing table
	return err != nil && strings.HasPrefix(err.Error(), "No connection pool")
}

func (server *ServerMonitor) GetJobCount(conn *sqlx.Conn, task string, state int) (int, error) {
	cluster := server.ClusterGroup

	// Query to count rows
	query := "SELECT COUNT(*) FROM replication_manager_schema.jobs WHERE task=? AND state=?"

	// Create the initial context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ReadTimeout)*time.Second)
	defer cancel() // Ensure the context is canceled when the function exits

	var count int

	// Attempt to get the job count
	err := conn.GetContext(ctx, &count, query, task, state)
	if err != nil {
		// Check if the table is missing
		if isTableMissingError(err) {
			// Try to create the jobs table
			if err2 := server.JobsCreateTable(); err2 != nil {
				return 0, fmt.Errorf("failed to create jobs table: %v", err2)
			}

			// Create a new context for retrying the query
			ctx, cancel = context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ReadTimeout)*time.Second)
			defer cancel() // Cancel the new context as well

			// Retry the query after creating the table
			err = conn.GetContext(ctx, &count, query, task, state)
			if err != nil {
				return 0, fmt.Errorf("failed to retrieve data on jobs table after retry: %v", err)
			}
		} else {
			return 0, fmt.Errorf("failed to retrieve data on jobs table: %v", err)
		}
	}

	// Return the count of matching rows
	return count, nil
}

// Get Tasks by state and done
func (server *ServerMonitor) GetTasksByState(conn *sqlx.Conn, args ...interface{}) ([]DBTask, error) {
	cluster := server.ClusterGroup
	tasks := make([]DBTask, 0)
	params := len(args)
	if params > 2 {
		return tasks, fmt.Errorf("Too many arguments for this function. Only state(int) and done(int) allowed. Received: %d", params)
	}

	query := "SELECT task ,count(*) as ct, max(id) as id FROM replication_manager_schema.jobs GROUP BY task"
	if params == 1 {
		query = "SELECT task ,count(*) as ct, max(id) as id FROM replication_manager_schema.jobs WHERE state=? GROUP BY task"
	} else if params == 2 {
		query = "SELECT task ,count(*) as ct, max(id) as id FROM replication_manager_schema.jobs WHERE state=? AND done=? GROUP BY task"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ReadTimeout)*time.Second)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		cancel()
		err2 := server.JobsCreateTable()
		if err2 != nil {
			return tasks, fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}

		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ReadTimeout)*time.Second)
		rows, err = conn.QueryContext(ctx, query)
		if err != nil {
			cancel()
			return tasks, fmt.Errorf("Failed to retrieve data on jobs table: %v", err)
		}
	}
	defer rows.Close()
	defer cancel()

	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct, &task.id)
		if task.ct > 0 {
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
}
