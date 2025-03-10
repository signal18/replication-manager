package cluster

// func (prx *Proxy) GetClusterConnection() (*sqlx.DB, error) {
// 	cluster := prx.ClusterGroup
// 	params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)
// 	dsn := cluster.GetDbUser() + ":" + cluster.GetDbPass() + "@"
// 	if cluster.Conf.MonitorWriteHeartbeatCredential != "" {
// 		dsn = cluster.Conf.GetDecryptedValue("monitoring-write-heartbeat-credential") + "@"
// 	}

// 	if prx.Host != "" {
// 		if prx.Tunnel {
// 			dsn += "tcp(localhost:" + strconv.Itoa(prx.TunnelWritePort) + ")/" + params
// 		} else {
// 			dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
// 		}
// 	}
// 	return sqlx.Open("mysql", dsn)

// }

// // All next query will not use binlog, except changing database via USE
// func (proxy *Proxy) GetSingleConn(db *sqlx.DB, timeout time.Duration) (*sqlx.Conn, error) {
// 	if db == nil {
// 		return nil, fmt.Errorf("No connection established")
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), timeout)
// 	defer cancel()

// 	conn, err := db.Connx(ctx)
// 	if err != nil {
// 		return nil, fmt.Errorf("Error getting single connection, %s", err)
// 	}

// 	return conn, nil
// }

// // This function will execute query and will use parameter for timeout
// func (proxy *Proxy) ConnExecQueryWithTimeout(conn *sqlx.Conn, timeout time.Duration, query string, args ...interface{}) (sql.Result, error) {
// 	if conn == nil {
// 		return nil, nil
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), timeout)
// 	defer cancel()

// 	res, err := conn.ExecContext(ctx, query, args...)
// 	if err != nil {
// 		return res, fmt.Errorf("Error exec query (%s): %s", query, err)
// 	}

// 	return res, nil
// }
