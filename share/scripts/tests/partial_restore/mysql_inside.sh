#!/bin/bash
# Runs inside a percona/percona-server container (as root).
# Usage: mysql_inside.sh setup [scenario] | restore <scenario> | exportcheck
# Failure scenarios: see scenarios.sh.
LVL_ERROR=ERROR LVL_WARN=WARN LVL_INFO=INFO LVL_DEBUG=DEBUG
export MYSQL_PWD=rootpw
CLI="mysql -uroot -h127.0.0.1 -P3306"
q() { $CLI -N -e "$1" 2>/dev/null; }
TABLES="prt_a.plain prt_a.gen prt_a.part prt_a.part_gen prt_a.parent prt_a.child prt_a.txt prt_a.gen_long prt_b.g2"
# Content hash of all rows (sorted): CHECKSUM TABLE is not stable for tables
# with STORED generated columns (it changes when the table is reopened).
checksums() { for t in $TABLES; do printf '%s ' "$t"; q "SELECT * FROM $t" | LC_ALL=C sort | md5sum | cut -c1-32; done; }
BACKUPDIR=/tmp/bk/b DATADIR=/var/lib/mysql
source /prtest/scenarios.sh

if [[ "$1" == "setup" ]]; then
    $CLI 2>&1 <<'SQL' | grep -i error
DROP DATABASE IF EXISTS prt_a; DROP DATABASE IF EXISTS prt_b;
CREATE DATABASE prt_a; CREATE DATABASE prt_b;
USE prt_a;
CREATE TABLE plain (id INT PRIMARY KEY, v VARCHAR(50)) ENGINE=InnoDB;
CREATE TABLE gen (id INT PRIMARY KEY, a INT, b INT, s INT AS (a+b) STORED, v INT AS (a*b) VIRTUAL, KEY (s), KEY (v)) ENGINE=InnoDB;
CREATE TABLE part (id INT, d DATE, v VARCHAR(20), PRIMARY KEY (id, d)) ENGINE=InnoDB
  PARTITION BY RANGE (YEAR(d)) (PARTITION p2025 VALUES LESS THAN (2026),
  PARTITION p2026 VALUES LESS THAN (2027), PARTITION pmax VALUES LESS THAN MAXVALUE);
CREATE TABLE part_gen (id INT, d DATE, y INT AS (YEAR(d)) VIRTUAL, PRIMARY KEY (id, d)) ENGINE=InnoDB
  PARTITION BY RANGE (YEAR(d)) (PARTITION p1 VALUES LESS THAN (2026), PARTITION p2 VALUES LESS THAN MAXVALUE);
CREATE TABLE parent (id INT PRIMARY KEY) ENGINE=InnoDB;
CREATE TABLE child (id INT PRIMARY KEY, pid INT, CONSTRAINT fk_child FOREIGN KEY (pid) REFERENCES parent(id) ON DELETE CASCADE) ENGINE=InnoDB;
CREATE TABLE txt (id INT PRIMARY KEY, t TEXT, u VARCHAR(255) CHARACTER SET utf8mb4, KEY (u)) ENGINE=InnoDB;
CREATE TABLE gen_long (id INT PRIMARY KEY, u VARCHAR(255) CHARACTER SET utf8mb4, t TEXT, g INT AS (id*2) STORED, v INT AS (id*3) VIRTUAL, KEY (u), KEY (v)) ENGINE=InnoDB;
CREATE TABLE my_t (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=MyISAM;
CREATE TABLE audit (id INT AUTO_INCREMENT PRIMARY KEY, msg VARCHAR(50)) ENGINE=InnoDB;
CREATE VIEW v_plain AS SELECT id, v FROM plain;
CREATE TRIGGER trg_plain AFTER INSERT ON plain FOR EACH ROW INSERT INTO audit (msg) VALUES (CONCAT('ins ', NEW.id));
CREATE FUNCTION f_double (x INT) RETURNS INT DETERMINISTIC RETURN x*2;
CREATE EVENT ev_tick ON SCHEDULE EVERY 1 DAY STARTS '2030-01-01 00:00:00' DO INSERT INTO audit (msg) VALUES ('tick');
INSERT INTO plain VALUES (1,'a'),(2,'b'),(5,'e');
INSERT INTO gen (id,a,b) VALUES (1,1,2),(2,3,4);
INSERT INTO part VALUES (1,'2025-01-01','x'),(2,'2026-06-01','y'),(3,'2030-01-01','z');
INSERT INTO part_gen (id,d) VALUES (1,'2025-02-02'),(2,'2027-02-02');
INSERT INTO parent VALUES (1),(2); INSERT INTO child VALUES (10,1),(20,2);
INSERT INTO txt VALUES (1, REPEAT('t',300), 'u1');
INSERT INTO gen_long (id,u,t) VALUES (1,'a','tt'),(2,'b','uu');
INSERT INTO my_t VALUES (1,'myisam');
USE prt_b;
CREATE TABLE g2 (id INT PRIMARY KEY, j JSON, k VARCHAR(20) AS (j->>'$.k') VIRTUAL) ENGINE=InnoDB;
INSERT INTO g2 (id,j) VALUES (1,'{"k":"one"}');
SQL
    sc_before_backup "${2:-keep-schema}"
    checksums >/tmp/before.txt
    exit 0
fi

if [[ "$1" == "exportcheck" ]]; then
    job=reseedxtrabackup
    LOG_DIR=/tmp/prlog; rm -rf "$LOG_DIR"; mkdir -p "$LOG_DIR"; PR_LOG="$LOG_DIR/reseed.out"
    BACKUPDIR=/tmp/bk/b DATADIR=/var/lib/mysql USER=root PASSWORD=rootpw
    BINARY_CLIENT="mysql -uroot -h127.0.0.1 -prootpw -P3306"; send_lines_to_api() { :; }
    source /prtest/functions.sh; isr=1; PR_DEFS="$BACKUPDIR/.mrm_defs"
    find "$BACKUPDIR" -path "$PR_DEFS" -prune -o -type f -print0 | xargs -0 sha256sum | sort -k2 >/tmp/sum.before
    pr_export_definitions; echo "export rc=$?"
    find "$BACKUPDIR" -path "$PR_DEFS" -prune -o -type f -print0 | xargs -0 sha256sum | sort -k2 >/tmp/sum.after
    echo "backup files: $(wc -l </tmp/sum.before) checked; $(diff -q /tmp/sum.before /tmp/sum.after >/dev/null && echo UNCHANGED || echo CHANGED)"
    diff /tmp/sum.before /tmp/sum.after | grep '^[<>]' | head -5
    echo "databases: $(tr '\n' ' ' <"$PR_DEFS/.databases" 2>/dev/null)"
    echo "exported: $(cat $PR_DEFS/*/.list 2>/dev/null | wc -l) objects, FKs=$(cat $PR_DEFS/*/*.sql 2>/dev/null | grep -c 'FOREIGN KEY') partitioned=$(cat $PR_DEFS/*/*.sql 2>/dev/null | grep -c 'PARTITION BY') generated=$(cat $PR_DEFS/*/*.sql 2>/dev/null | grep -c 'GENERATED ALWAYS') routines/events/triggers=$(cat $PR_DEFS/*/.routines 2>/dev/null | wc -l)"
    grep -E "Priming|read-only server|ERROR|No login" "$PR_LOG" | cut -c1-160 | head -8
    exit 0
fi

SCENARIO="${2:-keep-schema}"
sc_after_prepare "$SCENARIO"
# ---- diverge the live data so a no-op restore cannot pass ----------------
$CLI -e "CREATE DATABASE prt_extra; DROP FUNCTION prt_a.f_double; DROP EVENT prt_a.ev_tick; DROP VIEW prt_a.v_plain;" 2>/dev/null
$CLI -e "INSERT INTO prt_a.plain VALUES (3,'after'); UPDATE prt_a.gen SET a=100 WHERE id=1; INSERT INTO prt_a.part VALUES (4,'2025-05-05','after'); DELETE FROM prt_b.g2; UPDATE prt_a.my_t SET v='after';" 2>/dev/null
if [[ "$SCENARIO" == "no-schema" ]]; then
    $CLI -e "SET foreign_key_checks=0; DROP DATABASE prt_a; DROP DATABASE prt_b;" 2>/dev/null
fi
if [[ "$SCENARIO" == "leftovers" ]]; then
    cp /tmp/bk/b/prt_a/plain.ibd /var/lib/mysql/prt_a/stale_orphan.ibd; chown mysql:mysql /var/lib/mysql/prt_a/stale_orphan.ibd
fi

# ---- run partialRestore with dbjobs globals ------------------------------
job=reseedxtrabackup
LOG_DIR=/tmp/prlog; rm -rf "$LOG_DIR"; mkdir -p "$LOG_DIR"
cat /tmp/bk/prepare.log > "$LOG_DIR/reseed.out"
BACKUPDIR=/tmp/bk/b
DATADIR=/var/lib/mysql
USER=root PASSWORD=rootpw MYSQL_SERVER=127.0.0.1 MYSQL_PORT=3306
DB_CONN_PARAMETERS="-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT"
BINARY_CLIENT="mysql $DB_CONN_PARAMETERS"
JOBS_MODE=api ID=""
PR_STATUS=0 PR_LOG=""
send_lines_to_api() { :; }
source /prtest/functions.sh
sc_before_restore "$SCENARIO"
sc_state >/tmp/pre.txt
[[ "$SCENARIO" == "interrupted" ]] && sc_interrupted_first_run
partialRestore; rc=$?

checksums >/tmp/after.txt
echo "partialRestore rc=$rc"
echo "--- error summary (reseed.out)"
grep -E '^ERROR' "$LOG_DIR/reseed.out" | sed -E 's/[a-z_0-9]+\.[a-z_0-9#]+/X/g' | sort | uniq -c | sort -rn | head -8
echo "--- table checksums before-backup vs after-restore"
paste /tmp/before.txt /tmp/after.txt | awk '{s=($2==$4)?"OK":"MISMATCH"; print s, $1, $2, $4}'
echo "--- leftovers in datadir"
for f in $DATADIR/prt_a/*.ibd $DATADIR/prt_b/*.ibd; do [[ -e "$f" ]] || continue; n=$(basename "$f" .ibd); n=${n%%#*}; q "SELECT 1 FROM information_schema.tables WHERE table_schema='$(basename "$(dirname "$f")")' AND table_name='$n'" | grep -q 1 || echo "ORPHAN $f"; done
echo "--- integrity of non-table objects"
echo "view v_plain:      $(q "SELECT COUNT(*) FROM information_schema.views WHERE table_schema='prt_a' AND table_name='v_plain'")"
echo "trigger trg_plain: $(q "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema='prt_a' AND trigger_name='trg_plain'")"
echo "function f_double: $(q "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema='prt_a' AND routine_name='f_double'")"
echo "event ev_tick:     $(q "SELECT COUNT(*) FROM information_schema.events WHERE event_schema='prt_a' AND event_name='ev_tick'")"
echo "foreign key:       $(q "SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema='prt_a' AND table_name='child'")"
echo "myisam my_t:       $(q "SELECT v FROM prt_a.my_t WHERE id=1")"
# The server's own internal folders must never be touched by the restore
# (redo files rotate by name on their own, so presence is what is checked).
echo "redo files present: $(ls /var/lib/mysql/#innodb_redo 2>/dev/null | wc -l); internal folders quarantined: $(find /var/lib/mysql/.system -path '*orphan-quarantine*#innodb*' 2>/dev/null | wc -l)"
echo "--- quarantine"
find /var/lib/mysql/.system -path '*orphan-quarantine*' -type f 2>/dev/null
echo "--- skipped/reported"
grep -E 'SKIPPED|aborted|No login' "$LOG_DIR/reseed.out" | head -10
cp "$LOG_DIR/reseed.out" /prtest-out/reseed.out 2>/dev/null
sc_verdict "$SCENARIO" "$rc"
