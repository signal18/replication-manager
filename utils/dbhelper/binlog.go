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
		return counter, oldest, trimmed, query, fmt.Errorf("ERROR: QUERY_RESPONSE_TIME not available on PostgeSQL")
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

	lastpos := "4"
	exitloop := true
	logs := ""
	for exitloop {
		events := []BinlogEvents{}
		sql := "show binlog events IN '" + lastfile + "'  from " + lastpos + " LIMIT 60"
		logs += sql + "\n"
		err := db.Select(&events, sql)
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
			binlogindex, _ := strconv.Atoi(strings.Split(lastfile, ".")[1])
			binlogindex = binlogindex - 1
			lastfile = strings.Split(lastfile, ".")[0] + "." + fmt.Sprintf("%06d", binlogindex)
			lastpos = "4"
		}
	}
	return "", "", logs, errors.New("Not found Psudo GTID")
}

func GetBinlogPosAfterSkipNumberOfEvents(db *sqlx.DB, file string, pos string, skip int) (string, string, string, error) {

	events := []BinlogEvents{}
	sql := "show binlog events IN '" + file + "'  from " + pos + " LIMIT " + strconv.Itoa(skip)

	err := db.Select(&events, sql)
	if err != nil {
		return "", "", sql, err
	}
	if len(events) == 0 {
		return "", "", sql, err
	}
	return events[(len(events) - 1)].Log_name, strconv.FormatUint(uint64(events[(len(events)-1)].Pos), 10), sql, err
}

func GetNumberOfEventsAfterPos(db *sqlx.DB, lastfile string, lastpos string) (int, string, error) {

	exitloop := true
	logs := ""
	ct := 0
	for exitloop {
		events := []BinlogEvents{}
		sql := "show binlog events IN '" + lastfile + "'  from " + lastpos + " LIMIT 1"
		logs += sql + "\n"
		err := db.Select(&events, sql)
		if err != nil {
			return 0, logs, err
		}

		for _, row := range events {
			lastfile = strconv.FormatUint(uint64(row.End_log_pos), 10)
		}
		if len(events) == 0 {
			return ct, logs, nil
		}
		ct = ct + 1
	}
	return 0, logs, errors.New("Not found Psudo GTID")
}

func HaveExtraEvents(db *sqlx.DB, file string, pos string) (bool, string, error) {
	db.MapperFunc(strings.Title)
	evts := []BinlogEvents{}
	udb := db.Unsafe()
	stmt := "SHOW BINLOG EVENTS IN '" + file + "' FROM " + pos
	err := udb.Get(&evts, stmt)
	if err != nil {
		return true, stmt, err
	}
	if len(evts) == 1 {
		return false, stmt, nil
	}
	if len(evts) > 1 {
		return true, stmt, nil
	}
	return false, stmt, nil
}

func SetBinlogFormat(db *sqlx.DB, format string) (string, error) {
	query := "set global binlog_format='" + format + "'"
	_, err := db.Exec(query)
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
	query := "PURGE BINARY LOGS TO '" + filename + "'"

	if filename != "" {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid filename for PURGE BINARY LOGS TO")
	}
	return query, err
}

func PurgeBinlogBefore(db *sqlx.DB, ts int64) (string, error) {
	var err error
	var tstring string = time.Unix(ts, 0).Format(DDMMYYYYhhmmss)
	query := "PURGE BINARY LOGS BEFORE '" + tstring + "'"
	_, err = db.Exec(query)
	return query, err
}

func SetMaxBinlogTotalSize(db *sqlx.DB, size int) (string, error) {
	var err error
	query := "SET GLOBAL max_binlog_total_size = " + strconv.Itoa(size) + ""

	if size >= 0 {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid size for max_binlog_total_size")
	}
	return query, err
}

func SetSlaveConnectionsNeededForPurge(db *sqlx.DB, size int) (string, error) {
	var err error
	query := "SET GLOBAL slave_connections_needed_for_purge = " + strconv.Itoa(size) + ""

	if size >= 0 {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid value for slave_connections_needed_for_purge")
	}
	return query, err
}

func GetBinlogFormatDesc(db *sqlx.DB, binlogfile string) ([]BinlogEvents, string, error) {
	logs := ""
	logpos := "0"
	events := []BinlogEvents{}

	sql := fmt.Sprintf("show binlog events IN '%s' from %s LIMIT 3", binlogfile, logpos)
	logs += sql + "\n"
	err := db.Select(&events, sql)
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
		return counter, fmt.Errorf("ERROR: SHOW BINARY LOGS not available on PostgeSQL")
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

