package cluster

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

func TestMysqldumpExecRemote(t *testing.T) {
	cases := []struct {
		name       string
		execRemote string
		mode       string
		splitdump  bool
		remote     bool
	}{
		{"not asked", "", "api", false, false},
		{"other task asked", "optimize", "api", false, false},
		{"api mode", "mysqldump", "api", false, true},
		{"sql mode", "mysqldump", "sql", false, true},
		{"splitdump", "mysqldump", "api", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, server := newTestClusterServer(t)
			cl.Conf.SchedulerJobsExecRemote = c.execRemote
			cl.Conf.SchedulerJobsMode = c.mode
			cl.Conf.BackupMysqldumpSplitDump = c.splitdump
			cl.Conf.ParseJobsExecOverrides()
			if remote := server.mysqldumpExecRemote(); remote != c.remote {
				t.Fatalf("mysqldumpExecRemote() = %v, want %v", remote, c.remote)
			}
		})
	}
}

func TestOpenRemoteDumpReceiverSplitdumpSink(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string) // the receiver gives its port back here
	task := string(config.ConstTaskDump)
	done := server.armStreamTask(task)
	var got bytes.Buffer
	var mu sync.Mutex
	server.streamTasks.Store(remoteDumpSinkKey(task), io.Writer(writerFunc(func(b []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return got.Write(b)
	})))
	port, err := server.OpenRemoteDumpReceiver(task)
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	dump := strings.Repeat("INSERT INTO t VALUES (1);\n", 10000)
	c.Write([]byte(dump))
	c.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the stream must end the wait")
	}
	mu.Lock()
	defer mu.Unlock()
	if got.String() != dump {
		t.Fatalf("the splitdump sink got %d bytes, want %d", got.Len(), len(dump))
	}
}

func TestDumpBinlogRegexConnectionName(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	binlogRegex, _ := server.getBackupRegexPatterns()
	for _, line := range []string{
		"-- CHANGE MASTER '' TO MASTER_LOG_FILE='binlog.000016', MASTER_LOG_POS=355;", // mariadb-dump 10.11
		"-- CHANGE MASTER TO MASTER_LOG_FILE='binlog.000016', MASTER_LOG_POS=355;",    // mariadb-dump 11.4
	} {
		m := binlogRegex.FindStringSubmatch(line)
		if len(m) < 3 || m[1] != "binlog.000016" || m[2] != "355" {
			t.Fatalf("%q: got %q", line, m)
		}
	}
}

func TestDumpClientRegex(t *testing.T) {
	for line, want := range map[string]string{
		"-- MariaDB dump 10.19  Distrib 10.11.19-MariaDB, for debian-linux-gnu (x86_64)":                         "10.11.19",
		"/*M!999999\\- enable the sandbox mode */ \n-- MariaDB dump 10.19-11.4.13-MariaDB, for debian-linux-gnu": "11.4.13",
		"-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)":                                                "8.0.36",
	} {
		m := dumpClientRegex.FindStringSubmatch(line)
		if m == nil || m[1] != want {
			t.Fatalf("%q: got %q, want %s", line, m, want)
		}
	}
}

func TestStreamObserver(t *testing.T) {
	_, server := newTestClusterServer(t)
	task := string(config.ConstTaskDump)
	if server.streamObserver(task) != nil {
		t.Fatal("no backup reads the stream: no observer")
	}
	var got []byte
	server.streamTasks.Store(streamObserverKey(task), func(b []byte) { got = append(got, b...) })
	server.streamObserver(task)([]byte("x"))
	if string(got) != "x" {
		t.Fatalf("observer not called: %q", got)
	}
	var nilServer *ServerMonitor
	if nilServer.streamObserver(task) != nil {
		t.Fatal("a receiver without a server has no observer")
	}
}

func TestWaitStreamTaskArmed(t *testing.T) {
	_, server := newTestClusterServer(t)
	task := string(config.ConstTaskDump)
	if server.WaitStreamTaskArmed(task, 300*time.Millisecond) {
		t.Fatal("an unarmed task must not get a receiver")
	}
	server.armStreamTask(task)
	if !server.WaitStreamTaskArmed(task, time.Second) {
		t.Fatal("an armed task must get a receiver")
	}
}

func TestCloseRemoteDumpReceiverNotTaken(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string) // the receiver gives its port back here
	task := string(config.ConstTaskDump)
	done := server.armStreamTask(task)
	port, err := cl.SSTRunReceiverToGZip(server, t.TempDir()+"/dump.sql.gz", ConstJobCreateFile, task)
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	cl.stopRemoteDumpReceiver(port) // dbjobs never connected
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a closed receiver must end the wait for its stream")
	}
	p, _ := strconv.Atoi(port)
	SSTs.Lock()
	_, open := SSTs.SSTconnections[p]
	SSTs.Unlock()
	if open {
		t.Fatal("a closed receiver must free its port")
	}
}

func TestDumpPositionLinesTables(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	br, bg := server.getBackupRegexPatterns()
	dump := "-- CHANGE MASTER '' TO MASTER_LOG_FILE='binlog.000016', MASTER_LOG_POS=355;\n" +
		"-- SET GLOBAL gtid_slave_pos='0-1-99';\n" + // the position is found: the parser stops
		"--\n-- Current Database: `tpcc`\n--\n" +
		"-- Table structure for table `customer`\n" +
		"INSERT INTO `customer` VALUES " + strings.Repeat("(1,'-- Table structure for table `x`'),", 2000) + "(2,'y');\n" +
		"-- Current Database: `odd``name`\n" +
		"-- Table structure for table `item`\n" +
		"-- Temporary table structure for view `v`\n"
	for _, size := range []int{1, 7, 8192} {
		var got []string
		l := &dumpPositionLines{
			parser:  newDumpStreamParser(br, bg, true, true, func(string, uint64) {}, func(string) {}),
			onTable: func(schema, table string) { got = append(got, schema+"."+table) },
		}
		for off := 0; off < len(dump); off += size {
			l.Write([]byte(dump[off:min(off+size, len(dump))]))
		}
		l.Flush()
		if strings.Join(got, ",") != "tpcc.customer,odd`name.item" {
			t.Fatalf("chunk %d: tables %q", size, got)
		}
	}
}

func TestStopRemoteDumpStreamCutsOpenStream(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.SstAvailablePorts = make(map[string]string)
	task := string(config.ConstTaskDump)
	done := server.armStreamTask(task)
	server.streamTasks.Store(remoteDumpSinkKey(task), io.Writer(io.Discard))
	port, err := server.OpenRemoteDumpReceiver(task) // as receive-task does in api mode
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	c, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	c.Write([]byte("-- MariaDB dump\n")) // then the stream stays open, idle
	time.Sleep(200 * time.Millisecond)
	server.stopRemoteDumpStream(task) // the backup is cancelled or stalled
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the backup must cut a stream it no longer waits for")
	}
}

func TestWaitRemoteJobReport(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.JobResults = config.NewTasksMap()
	task := string(config.ConstTaskDump)

	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateSuccess, Done: 1})
	if err := server.waitRemoteJobReport(t.Context(), task, time.Second); err != nil {
		t.Fatalf("done report: %v", err)
	}
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateErrorExec, Done: 1, Result: "error"})
	if err := server.waitRemoteJobReport(t.Context(), task, time.Second); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error report: %v", err)
	}
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateRunning})
	if err := server.waitRemoteJobReport(t.Context(), task, 1500*time.Millisecond); err == nil {
		t.Fatal("no report must time out")
	}
}

func TestDumpPositionLinesAcrossChunks(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	br, bg := server.getBackupRegexPatterns()
	var bfile, bgtid string
	var bpos uint64
	l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, true, true,
		func(f string, p uint64) { bfile, bpos = f, p }, func(g string) { bgtid = g })}
	dump := "-- MariaDB dump 10.19  Distrib 10.11.19-MariaDB\n" +
		"-- CHANGE MASTER '' TO MASTER_LOG_FILE='binlog.000016', MASTER_LOG_POS=355;\n" +
		"INSERT INTO `t` VALUES " + strings.Repeat("(1,'x'),", 20000) + "(2,'gtid CHANGE ');\n" +
		"-- SET GLOBAL gtid_slave_pos='0-1-99';"
	for _, size := range []int{1, 7, 8192} {
		bfile, bpos, bgtid = "", 0, ""
		l.parser.wantBinlog, l.parser.wantGTID = true, true
		for off := 0; off < len(dump); off += size {
			l.Write([]byte(dump[off:min(off+size, len(dump))]))
		}
		l.Flush()
		if bfile != "binlog.000016" || bpos != 355 || bgtid != "0-1-99" {
			t.Fatalf("chunk %d: got %q %d %q", size, bfile, bpos, bgtid)
		}
	}
}

func TestDumpPositionLinesLongGTID(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	br, bg := server.getBackupRegexPatterns()
	var bgtid string
	l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, false, true, nil, func(g string) { bgtid = g })}
	var domains []string
	for d := 0; d < 20000; d++ {
		domains = append(domains, fmt.Sprintf("%d-1-%d", d, d*7))
	}
	want := strings.Join(domains, ",") // well past dumpPositionLineHead and any fixed cap
	dump := "INSERT INTO `t` VALUES (1);\n-- SET GLOBAL gtid_slave_pos='" + want + "';\n"
	for off := 0; off < len(dump); off += 8192 {
		l.Write([]byte(dump[off:min(off+8192, len(dump))]))
	}
	l.Flush()
	if bgtid != want {
		t.Fatalf("long GTID set cut: got %d bytes, want %d", len(bgtid), len(want))
	}
}

func TestDumpGTIDRegexMySQL8(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}
	_, gtidRegex := server.getBackupRegexPatterns()
	for line, want := range map[string]string{
		"SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '09b71b9f-bfd9-11f1-9755-f6a681adff9d:1-5';": "09b71b9f-bfd9-11f1-9755-f6a681adff9d:1-5", // mysqldump 8.0
		"SET @@GLOBAL.GTID_PURGED=/*!80000 '3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19'*/;":    "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19",
		"SET @@GLOBAL.GTID_PURGED='3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19';":               "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19", // 5.7
		"SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '09b71b9f-bfd9-11f1-9755-f6a681adff9d:1,":    "",                                          // cut: never the '+'
	} {
		got := ""
		if m := gtidRegex.FindStringSubmatch(line); m != nil {
			got = firstNonEmpty(m[1:])
		}
		if got != want {
			t.Fatalf("%q: got %q, want %q", line, got, want)
		}
	}
}

func TestDumpPositionLinesMySQLMultiLineGTID(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}
	br, bg := server.getBackupRegexPatterns()
	// as mysqldump 8.0.35 writes it on a replica (lab, repmy8)
	head := "-- MySQL dump 10.13  Distrib 8.0.35, for Linux (x86_64)\n" +
		"SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '09b71b9f-bfd9-11f1-9755-f6a681adff9d:1,\n" +
		"09d4a607-bfd9-11f1-8971-cacfb4ebda27:1-447';\n\n" +
		"CHANGE MASTER TO MASTER_LOG_FILE='binlog.000018', MASTER_LOG_POS=197;\n"
	data := "INSERT INTO `t` VALUES " + strings.Repeat("(1,'x'),", 20000) + "(2,'y');\n"
	for _, size := range []int{1, 7, 8192} {
		var bfile, bgtid string
		l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, true, true,
			func(f string, p uint64) { bfile = f }, func(g string) { bgtid = g })}
		dump := head + data
		for off := 0; off < len(dump); off += size {
			l.Write([]byte(dump[off:min(off+size, len(dump))]))
		}
		l.Flush()
		if bgtid != "09b71b9f-bfd9-11f1-9755-f6a681adff9d:1,09d4a607-bfd9-11f1-8971-cacfb4ebda27:1-447" || bfile != "binlog.000018" {
			t.Fatalf("chunk %d: gtid %q binlog %q", size, bgtid, bfile)
		}
	}
	// indented continuation lines and CRLF line ends
	for _, nl := range []string{"\n", "\r\n"} {
		var bgtid string
		l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, false, true, nil, func(g string) { bgtid = g })}
		l.Write([]byte("SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'aaaaaaaa-bfd9-11f1-9755-f6a681adff9d:1-2," + nl +
			"  bbbbbbbb-bfd9-11f1-8971-cacfb4ebda27:1-447," + nl + "\tcccccccc-bfd9-11f1-8971-cacfb4ebda27:5';" + nl + data))
		l.Flush()
		if bgtid != "aaaaaaaa-bfd9-11f1-9755-f6a681adff9d:1-2,bbbbbbbb-bfd9-11f1-8971-cacfb4ebda27:1-447,cccccccc-bfd9-11f1-8971-cacfb4ebda27:5" {
			t.Fatalf("line end %q: gtid %q", nl, bgtid)
		}
	}
	// a statement cut by another line: no GTID (never the '+'), and the line is not kept
	var bgtid string
	l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, false, true, nil, func(g string) { bgtid = g })}
	l.Write([]byte("SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '09b71b9f-bfd9-11f1-9755-f6a681adff9d:1,\n" + data))
	if bgtid != "" || l.stmt != nil || len(l.head) > dumpPositionLineHead {
		t.Fatalf("cut statement: gtid %q, statement kept %v, line buffer %d", bgtid, l.stmt != nil, len(l.head))
	}
}

func TestDumpPositionLinesMySQLManySources(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MySQL", Major: 8, Minor: 4}
	br, bg := server.getBackupRegexPatterns()
	var sources []string
	for i := 0; i < 10000; i++ {
		// several intervals per source, and an 8.4 tagged GTID
		sources = append(sources, fmt.Sprintf("%08x-bfd9-11f1-9755-f6a681adff9d:1-%d:%d-%d:batch_%d:1-9", i, i+5, i+7, i+90, i))
	}
	want := strings.Join(sources, ",")
	dump := "SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '" + strings.Join(sources, ",\n") + "';\n" +
		"INSERT INTO `t` VALUES (1);\n"
	var bgtid string
	l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, false, true, nil, func(g string) { bgtid = g })}
	for off := 0; off < len(dump); off += 8192 {
		l.Write([]byte(dump[off:min(off+8192, len(dump))]))
	}
	l.Flush()
	if bgtid != want {
		t.Fatalf("10000 sources: got %d bytes, want %d", len(bgtid), len(want))
	}
}

func TestDumpPositionLinesCeiling(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}
	br, bg := server.getBackupRegexPatterns()
	huge := strings.Repeat("09b71b9f-bfd9-11f1-9755-f6a681adff9d:1-2:4-6:8-10,", dumpPositionLineMax/48+1)
	for name, gtidLines := range map[string]string{
		"one line":   "SET @@GLOBAL.GTID_PURGED='" + huge + "1-1';\n",
		"statement":  "SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'aaaaaaaa-bfd9-11f1-9755-f6a681adff9d:1,\n" + strings.ReplaceAll(huge, ",", ",\n") + "bbbbbbbb-bfd9-11f1-9755-f6a681adff9d:1';\n",
		"no newline": "SET @@GLOBAL.GTID_PURGED='" + huge + huge, // a stream that never ends the line
	} {
		var bfile, bgtid string
		l := &dumpPositionLines{parser: newDumpStreamParser(br, bg, true, true,
			func(f string, p uint64) { bfile = f }, func(g string) { bgtid = g })}
		dump := gtidLines + "\nCHANGE MASTER TO MASTER_LOG_FILE='binlog.000018', MASTER_LOG_POS=197;\n"
		for off := 0; off < len(dump); off += 8192 {
			l.Write([]byte(dump[off:min(off+8192, len(dump))]))
			if len(l.stmt)+len(l.head) > dumpPositionLineMax || cap(l.stmt)+cap(l.head) > dumpPositionLineMax*5/4+8192 {
				t.Fatalf("%s: buffer past the ceiling: %d", name, len(l.stmt)+cap(l.head))
			}
		}
		l.Flush()
		if bgtid != "" || l.dropped != 1 || l.stmt != nil || cap(l.head) > dumpPositionLineHead*16 {
			t.Fatalf("%s: gtid %q, dropped %d, statement kept %v, buffer %d", name, bgtid, l.dropped, l.stmt != nil, cap(l.head))
		}
		if name != "no newline" && bfile != "binlog.000018" {
			t.Fatalf("%s: the position after a dropped line must still be read, got %q", name, bfile)
		}
	}
}

func TestGetRemoteMysqlDumpOptionsDropsConnection(t *testing.T) {
	cl, server := newTestClusterServer(t)
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	cl.VersionsMap = config.NewVersionsMap()
	server.Variables = config.NewStringsMap()
	cl.Conf.BackupMysqldumpOptions = "--hex-blob --user=evil -u root2 --password=secret1 -psecret2 --host dbx -hdby -P 3307 " +
		"--ssl-ca /etc/ca.pem --ssl --ssl-verify-server-cert --single-transaction --defaults-file=/x --socket /s -S/t " +
		"--protocol=SOCKET --login-path=lp --routines"
	args, dropped := cl.GetRemoteMysqlDumpOptions(server)
	joined := strings.Join(args, " ")
	for _, leak := range []string{"evil", "root2", "secret1", "secret2", "dbx", "dby", "3307", "/etc/ca.pem", "/x", "/s", "/t", "SOCKET", "lp", "--ssl", "--user", "--password", "--host", "--port", "--socket", "--protocol", "--defaults-file", "--login-path"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("%q reaches the DB node arguments: %s", leak, joined)
		}
	}
	for _, keep := range []string{"--hex-blob", "--single-transaction", "--routines"} {
		if !slices.Contains(args, keep) {
			t.Fatalf("%s must be kept: %s", keep, joined)
		}
	}
	names := strings.Join(dropped, " ")
	for _, v := range []string{"secret1", "secret2", "evil", "root2"} {
		if strings.Contains(names, v) {
			t.Fatalf("the dropped names must never carry a value: %s", names)
		}
	}
	if !strings.Contains(names, "--password") || !strings.Contains(names, "-p") || !strings.Contains(names, "-u") {
		t.Fatalf("dropped options not named: %s", names)
	}
}

func TestSecondsOr(t *testing.T) {
	if got := secondsOr(0, time.Minute); got != time.Minute {
		t.Fatalf("unset: %s", got)
	}
	if got := secondsOr(-5, time.Minute); got != time.Minute {
		t.Fatalf("negative: %s", got)
	}
	if got := secondsOr(900, time.Minute); got != 15*time.Minute {
		t.Fatalf("set: %s", got)
	}
}
