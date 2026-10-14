package splitdump

import "testing"

func TestGtidStatementLines(t *testing.T) {
	feed := func(lines ...string) []string {
		var g gtidStatementLines
		var out []string
		for _, l := range lines {
			if s := g.add(l); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	gtid := func(stmts []string) string {
		for _, s := range stmts {
			if m := gtidRegexMySQL.FindStringSubmatch(s); m != nil {
				return m[1]
			}
		}
		return ""
	}
	// one source: one line, as is
	if got := gtid(feed("SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'a1:1-3';\n")); got != "a1:1-3" {
		t.Errorf("one line: %q", got)
	}
	// several sources: one per line, joined
	if got := gtid(feed("SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'a1:1-3,\n", "b2:1-5,\n", "c3:1-7';\n")); got != "a1:1-3,b2:1-5,c3:1-7" {
		t.Errorf("several lines: %q", got)
	}
	// a cut statement (the next line is not a GTID set part): no position, the line is kept
	out := feed("SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'a1:1-3,\n", "-- Current Database: `db`\n")
	if got := gtid(out); got != "" || len(out) != 1 || out[0] != "-- Current Database: `db`" {
		t.Errorf("cut statement: gtid %q, lines %q", got, out)
	}
	// other lines pass through
	if out := feed("-- CHANGE MASTER TO MASTER_LOG_FILE='binlog.000003', MASTER_LOG_POS=285;\n"); len(out) != 1 {
		t.Errorf("other line: %q", out)
	}
}
