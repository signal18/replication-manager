// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func GetBinaryLogs(db *sqlx.DB, version *version.Version, metamap *BinaryLogMetaMap) (int, string, []string, string, error) {
	counter := 0
	oldest := ""
	trimmed := make([]string, 0)
	query := "SHOW BINARY LOGS"
	if version.IsPostgreSQL() {
		return counter, oldest, trimmed, query, fmt.Errorf("ERROR: QUERY_RESPONSE_TIME not available on PostgreSQL")
	}
	rows, err := db.Queryx(query)

	if err != nil {
		return counter, oldest, trimmed, query, errors.New("Could not get binary logs: " + err.Error())
	}
	defer rows.Close()
	for rows.Next() {
		var v Binarylogs
		if version.IsMySQLOrPercona() && version.Major >= 8 {
			err = rows.Scan(&v.Log_name, &v.File_size, &v.Encrypted)
		} else {
			err = rows.Scan(&v.Log_name, &v.File_size)
		}
		if err != nil {
			return counter, oldest, trimmed, query, errors.New("Could not get binary logs: " + err.Error())
		}
		if oldest == "" {
			oldest = v.Log_name
		}
		if meta, exists := metamap.LoadOrStore(v.Log_name, &BinaryLogMetadata{Filename: v.Log_name, Size: v.File_size}); exists {
			if meta.Size != v.File_size {
				meta.Size = v.File_size
			}
		}
		counter++
	}

	trimmed = metamap.ClearObsoleteMetadata(oldest)

	return counter, oldest, trimmed, query, nil
}

func GetLastPseudoGTID(db *sqlx.DB) (string, string, error) {
	var value string
	value = ""
	query := "select * from replication_manager_schema.pseudo_gtid_v"
	err := db.QueryRowx(query).Scan(&value)
	return value, query, err
}

func GetBinlogEventPseudoGTID(db *sqlx.DB, uuid string, lastfile string) (string, string, string, error) {
	// Validate binlog filename to prevent injection
	if err := ValidateFilename(lastfile); err != nil {
		return "", "", "", fmt.Errorf("invalid binlog filename: %w", err)
	}

	lastpos := "4"
	logs := ""

	// Loop backwards through binlog files searching for pseudo-GTID marker
	// Exit conditions:
	//   1. UUID found in event info (returns found position)
	//   2. Database error (returns error)
	//   3. Binlog index becomes negative (returns error - reached first binlog)
	//   4. Invalid filename after decrement (returns error)
	for {
		events := []BinlogEvents{}

		// Validate position is numeric
		if err := ValidateNumeric(lastpos); err != nil {
			return "", "", logs, fmt.Errorf("invalid position: %w", err)
		}

		sql := "SHOW BINLOG EVENTS IN ? FROM ? LIMIT 60"
		logs += fmt.Sprintf("SHOW BINLOG EVENTS IN '%s' FROM %s LIMIT 60\n", lastfile, lastpos)

		err := db.Select(&events, sql, lastfile, lastpos)
		if err != nil {
			return "", "", logs, err
		}

		for _, row := range events {
			pos := strconv.FormatUint(uint64(row.Pos), 10)
			endpos := strconv.FormatUint(uint64(row.End_log_pos), 10)
			if strings.Contains(row.Info, uuid) {
				return row.Log_name, pos, logs, err
			}
			lastpos = endpos
		}
		if len(events) == 0 {
			// Parse and decrement binlog index safely
			parts := strings.Split(lastfile, ".")
			if len(parts) != 2 {
				return "", "", logs, errors.New("invalid binlog filename format")
			}
			binlogindex, err := strconv.Atoi(parts[1])
			if err != nil {
				return "", "", logs, fmt.Errorf("invalid binlog index: %w", err)
			}
			binlogindex = binlogindex - 1
			if binlogindex < 0 {
				return "", "", logs, errors.New("binlog index cannot be negative")
			}
			lastfile = parts[0] + "." + fmt.Sprintf("%06d", binlogindex)

			// Validate the newly constructed filename
			if err := ValidateFilename(lastfile); err != nil {
				return "", "", logs, fmt.Errorf("invalid constructed binlog filename: %w", err)
			}
			lastpos = "4"
		}
	}
}

func GetBinlogPosAfterSkipNumberOfEvents(db *sqlx.DB, file string, pos string, skip int) (string, string, string, error) {
	// Validate binlog filename to prevent injection
	if err := ValidateFilename(file); err != nil {
		return "", "", "", fmt.Errorf("invalid binlog filename: %w", err)
	}

	// Validate position is numeric
	if err := ValidateNumeric(pos); err != nil {
		return "", "", "", fmt.Errorf("invalid position: %w", err)
	}

	// Validate skip is non-negative
	if skip < 0 {
		return "", "", "", errors.New("skip value cannot be negative")
	}

	events := []BinlogEvents{}
	sql := "SHOW BINLOG EVENTS IN ? FROM ? LIMIT ?"
	logQuery := fmt.Sprintf("SHOW BINLOG EVENTS IN '%s' FROM %s LIMIT %d", file, pos, skip)

	err := db.Select(&events, sql, file, pos, skip)
	if err != nil {
		return "", "", logQuery, err
	}
	if len(events) == 0 {
		return "", "", logQuery, err
	}
	return events[(len(events) - 1)].Log_name, strconv.FormatUint(uint64(events[(len(events)-1)].Pos), 10), logQuery, err
}

func GetNumberOfEventsAfterPos(db *sqlx.DB, lastfile string, lastpos string) (int, string, error) {
	// Validate binlog filename to prevent injection
	if err := ValidateFilename(lastfile); err != nil {
		return 0, "", fmt.Errorf("invalid binlog filename: %w", err)
	}

	// Validate position is numeric
	if err := ValidateNumeric(lastpos); err != nil {
		return 0, "", fmt.Errorf("invalid position: %w", err)
	}

	logs := ""
	ct := 0

	// Loop through remaining binlog events counting them
	// Exit conditions:
	//   1. No more events in binlog (returns count)
	//   2. Database error (returns error)
	for {
		events := []BinlogEvents{}
		sql := "SHOW BINLOG EVENTS IN ? FROM ? LIMIT 1"
		logQuery := fmt.Sprintf("SHOW BINLOG EVENTS IN '%s' FROM %s LIMIT 1\n", lastfile, lastpos)
		logs += logQuery

		err := db.Select(&events, sql, lastfile, lastpos)
		if err != nil {
			return 0, logs, err
		}

		for _, row := range events {
			lastpos = strconv.FormatUint(uint64(row.End_log_pos), 10)
		}
		if len(events) == 0 {
			return ct, logs, nil
		}
		ct = ct + 1
	}
}

func HaveExtraEvents(db *sqlx.DB, file string, pos string) (bool, string, error) {
	// Validate binlog filename to prevent injection
	if err := ValidateFilename(file); err != nil {
		return true, "", fmt.Errorf("invalid binlog filename: %w", err)
	}

	// Validate position is numeric
	if err := ValidateNumeric(pos); err != nil {
		return true, "", fmt.Errorf("invalid position: %w", err)
	}

	db.MapperFunc(strings.Title)
	evts := []BinlogEvents{}
	udb := db.Unsafe()
	stmt := "SHOW BINLOG EVENTS IN ? FROM ?"
	logQuery := fmt.Sprintf("SHOW BINLOG EVENTS IN '%s' FROM %s", file, pos)

	err := udb.Select(&evts, stmt, file, pos)
	if err != nil {
		return true, logQuery, err
	}
	if len(evts) == 1 {
		return false, logQuery, nil
	}
	if len(evts) > 1 {
		return true, logQuery, nil
	}
	return false, logQuery, nil
}

func SetBinlogFormat(db *sqlx.DB, format string) (string, error) {
	// Validate binlog format before using
	if err := ValidateBinlogFormat(format); err != nil {
		return "", err
	}
	query := "SET GLOBAL binlog_format = ?"
	_, err := db.Exec(query, format)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetBinlogAnnotate(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL binlog_annotate_row_events=ON"
	logs := query
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	query = "SET GLOBAL replicate_annotate_row_events=ON"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func SetBinlogSlowqueries(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL log_slow_slave_statements=ON"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetBinlogChecksum(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL binlog_checksum=1"
	logs := query
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}

	query = "SET GLOBAL master_verify_checksum=1"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func SetBinlogCompress(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL log_bin_compress=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func FlushBinaryLogsLocal(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH LOCAL BINARY LOGS")
	return "FLUSH LOCAL BINARY LOGS", err
}

func FlushBinaryLogs(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH  BINARY LOGS")
	return "FLUSH BINARY LOGS", err
}

func PurgeBinlogTo(db *sqlx.DB, filename string) (string, error) {
	var err error

	// Validate filename to prevent SQL injection and path traversal
	if err := ValidateFilename(filename); err != nil {
		return "", err
	}

	query := "PURGE BINARY LOGS TO ?"
	_, err = db.Exec(query, filename)
	return query, err
}

func PurgeBinlogBefore(db *sqlx.DB, ts int64) (string, error) {
	var err error
	var tstring string = time.Unix(ts, 0).Format(DDMMYYYYhhmmss)
	query := "PURGE BINARY LOGS BEFORE ?"
	_, err = db.Exec(query, tstring)
	return query, err
}

func SetMaxBinlogTotalSize(db *sqlx.DB, size int) (string, error) {
	var err error

	if size < 0 {
		return "", errors.New("Invalid size for max_binlog_total_size: must be >= 0")
	}

	query := "SET GLOBAL max_binlog_total_size = ?"
	_, err = db.Exec(query, size)
	return query, err
}

func SetSlaveConnectionsNeededForPurge(db *sqlx.DB, size int) (string, error) {
	var err error

	if size < 0 {
		return "", errors.New("Invalid value for slave_connections_needed_for_purge: must be >= 0")
	}

	query := "SET GLOBAL slave_connections_needed_for_purge = ?"
	_, err = db.Exec(query, size)
	return query, err
}

func GetBinlogFormatDesc(db *sqlx.DB, binlogfile string) ([]BinlogEvents, string, error) {
	// Validate binlog filename to prevent injection
	if err := ValidateFilename(binlogfile); err != nil {
		return nil, "", fmt.Errorf("invalid binlog filename: %w", err)
	}

	logs := ""
	logpos := "0"
	events := []BinlogEvents{}

	sql := "SHOW BINLOG EVENTS IN ? FROM ? LIMIT 3"
	logQuery := fmt.Sprintf("SHOW BINLOG EVENTS IN '%s' FROM %s LIMIT 3\n", binlogfile, logpos)
	logs += logQuery

	err := db.Select(&events, sql, binlogfile, logpos)
	if err != nil {
		return nil, logs, err
	}

	for _, row := range events {
		if strings.ToUpper(row.Event_type) == "FORMAT_DESC" {
			return []BinlogEvents{row}, logs, nil
		}
	}

	return nil, logs, errors.New("Binlog Format Desc Not Found")
}

func CountBinaryLogs(db *sqlx.DB, version *version.Version) (int, error) {
	counter := 0
	query := "SHOW BINARY LOGS"
	if version.IsPostgreSQL() {
		return counter, fmt.Errorf("ERROR: SHOW BINARY LOGS not available on PostgreSQL")
	}
	rows, err := db.Queryx(query)

	if err != nil {
		return counter, errors.New("Could not get binary logs: " + err.Error())
	}
	defer rows.Close()
	for rows.Next() {
		counter++
	}

	// Check for any error after iterating through rows
	if err = rows.Err(); err != nil {
		return counter, errors.New("Error iterating show binary logs: " + err.Error())
	}

	return counter, nil
}
