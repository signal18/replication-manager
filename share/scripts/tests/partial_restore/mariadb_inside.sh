#!/bin/bash
# Runs inside a mariadb:<version> container. Exercises partialRestore() from
# dbjobs_new.sh (extracted into /prtest/functions.sh) against a real
# mariadb-backup --backup / --prepare --export cycle on the same server.
# Usage: mariadb_inside.sh <scenario>   scenario: keep-schema | no-schema | leftovers,
#        or a failure scenario of scenarios.sh
LVL_ERROR=ERROR LVL_WARN=WARN LVL_INFO=INFO LVL_DEBUG=DEBUG
SCENARIO="${1:-keep-schema}"
export MYSQL_PWD=rootpw
CLI="mariadb -uroot -h127.0.0.1 -P3306"
BK=$(command -v mariadb-backup || command -v mariabackup)
q() { $CLI -N -e "$1" 2>/dev/null; }

# ---- test schema ---------------------------------------------------------
$CLI <<'SQL' 2>&1 | grep -i error
DROP DATABASE IF EXISTS prt_a; DROP DATABASE IF EXISTS prt_b;
CREATE DATABASE prt_a; CREATE DATABASE prt_b;
USE prt_a;
CREATE TABLE plain (id INT PRIMARY KEY, v VARCHAR(50)) ENGINE=InnoDB;
CREATE TABLE gen (id INT PRIMARY KEY, a INT, b INT,
  s INT AS (a+b) STORED, v INT AS (a*b) VIRTUAL, KEY (s)) ENGINE=InnoDB;
CREATE TABLE part (id INT, d DATE, v VARCHAR(20), PRIMARY KEY (id, d)) ENGINE=InnoDB
  PARTITION BY RANGE (YEAR(d)) (PARTITION p2025 VALUES LESS THAN (2026),
  PARTITION p2026 VALUES LESS THAN (2027), PARTITION pmax VALUES LESS THAN MAXVALUE);
CREATE TABLE part_gen (id INT, d DATE, y INT AS (YEAR(d)) VIRTUAL, PRIMARY KEY (id, d)) ENGINE=InnoDB
  PARTITION BY RANGE (YEAR(d)) (PARTITION p1 VALUES LESS THAN (2026), PARTITION p2 VALUES LESS THAN MAXVALUE);
CREATE TABLE parent (id INT PRIMARY KEY) ENGINE=InnoDB;
CREATE TABLE child (id INT PRIMARY KEY, pid INT, FOREIGN KEY (pid) REFERENCES parent(id)) ENGINE=InnoDB;
CREATE TABLE txt (id INT PRIMARY KEY, t TEXT, u VARCHAR(255) CHARACTER SET utf8mb4, KEY (u)) ENGINE=InnoDB;
CREATE TABLE gen_long (id INT PRIMARY KEY, u VARCHAR(255) CHARACTER SET utf8mb4, t TEXT, g INT AS (id*2) STORED, v INT AS (id*3) VIRTUAL, KEY (u), KEY (v)) ENGINE=InnoDB;
INSERT INTO plain VALUES (1,'a'),(2,'b');
INSERT INTO gen (id,a,b) VALUES (1,1,2),(2,3,4);
INSERT INTO part VALUES (1,'2025-01-01','x'),(2,'2026-06-01','y'),(3,'2030-01-01','z');
INSERT INTO part_gen (id,d) VALUES (1,'2025-02-02'),(2,'2027-02-02');
INSERT INTO parent VALUES (1),(2); INSERT INTO child VALUES (10,1),(20,2);
INSERT INTO txt VALUES (1, REPEAT('t',300), 'u1');
INSERT INTO gen_long (id,u,t) VALUES (1,'a','tt'),(2,'b','uu');
USE prt_b;
CREATE TABLE g2 (id INT PRIMARY KEY, j JSON, k VARCHAR(20) AS (JSON_VALUE(j,'$.k')) VIRTUAL) ENGINE=InnoDB;
INSERT INTO g2 (id,j) VALUES (1,'{"k":"one"}');
USE prt_a;
CREATE VIEW v_plain AS SELECT id, v FROM plain;
CREATE TABLE audit (id INT AUTO_INCREMENT PRIMARY KEY, msg VARCHAR(50)) ENGINE=InnoDB;
CREATE TRIGGER trg_plain AFTER INSERT ON plain FOR EACH ROW INSERT INTO audit (msg) VALUES (CONCAT('ins ', NEW.id));
CREATE FUNCTION f_double (x INT) RETURNS INT DETERMINISTIC RETURN x*2;
CREATE EVENT ev_tick ON SCHEDULE EVERY 1 DAY STARTS '2030-01-01 00:00:00' DO INSERT INTO audit (msg) VALUES ('tick');
CREATE SEQUENCE seq1 START WITH 100;
CREATE TABLE aria_t (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=Aria;
INSERT INTO aria_t VALUES (1,'aria');
INSERT INTO plain VALUES (5,'e');
CREATE USER 'bk_user'@'%' IDENTIFIED BY 'x'; GRANT SELECT ON prt_a.* TO 'bk_user'@'%';
SQL
q "SELECT NEXTVAL(prt_a.seq1)" >/dev/null
BACKUPDIR=/tmp/bk
source /prtest/scenarios.sh
sc_before_backup "$SCENARIO"

TABLES="prt_a.plain prt_a.gen prt_a.part prt_a.part_gen prt_a.parent prt_a.child prt_a.txt prt_a.gen_long prt_b.g2"
# Content hash of all rows (sorted): CHECKSUM TABLE is not stable for tables
# with STORED generated columns (it changes when the table is reopened).
checksums() { for t in $TABLES; do printf '%s ' "$t"; q "SELECT * FROM $t" | LC_ALL=C sort | md5sum | cut -c1-32; done; }
checksums >/tmp/before.txt

# ---- backup + prepare --export (retried: fresh containers can race) -------
$CLI -e "FLUSH TABLES" 2>/dev/null; sleep 2
for try in 1 2 3; do
    rm -rf /tmp/bk && mkdir -p /tmp/bk
    $BK --backup --user=root --password=rootpw --host=127.0.0.1 --port=3306 --target-dir=/tmp/bk >/tmp/bk_backup.log 2>&1 && break
    [[ $try -eq 3 ]] && { echo "BACKUP FAILED"; tail -5 /tmp/bk_backup.log; exit 2; }
    sleep 3
done
if [[ "$SCENARIO" == "prepare-failed" ]]; then
    echo "prepare not run by the test" >/tmp/bk_prepare.log
else
    $BK --prepare --export --target-dir=/tmp/bk >/tmp/bk_prepare.log 2>&1 || echo "(prepare exit $? - tolerated, as in dbjobs)"
fi
sc_after_prepare "$SCENARIO"

# ---- diverge the live data so a no-op restore cannot pass ----------------
$CLI -e "CREATE DATABASE prt_extra; CREATE TABLE prt_extra.t (id INT) ENGINE=InnoDB; DROP USER 'bk_user'@'%'; DROP FUNCTION prt_a.f_double; DROP EVENT prt_a.ev_tick; DROP VIEW prt_a.v_plain;" 2>/dev/null
$CLI -e "INSERT INTO prt_a.plain VALUES (3,'after'); UPDATE prt_a.gen SET a=100 WHERE id=1; INSERT INTO prt_a.part VALUES (4,'2025-05-05','after'); DELETE FROM prt_b.g2;" 2>/dev/null
if [[ "$SCENARIO" == "leftovers" ]]; then
    # Debris of an earlier failed partial restore, as found on lab db2: an
    # orphan tablespace and a BLACKHOLE-patched stub .frm the server cannot open.
    cp /tmp/bk/prt_a/gen.ibd /var/lib/mysql/prt_a/stale_orphan.ibd
    sed -e 's/\x06\x00\x49\x6E\x6E\x6F\x44\x42\x00\x00\x00/\x09\x00\x42\x4C\x41\x43\x4B\x48\x4F\x4C\x45/g' </tmp/bk/prt_a/gen.frm >/var/lib/mysql/prt_a/mrm_pivo.frm
    chown mysql:mysql /var/lib/mysql/prt_a/stale_orphan.ibd /var/lib/mysql/prt_a/mrm_pivo.frm
fi
if [[ "$SCENARIO" == "no-schema" ]]; then
    $CLI -e "SET foreign_key_checks=0; DROP DATABASE prt_a; DROP DATABASE prt_b;" 2>/dev/null
fi

# ---- run partialRestore with dbjobs globals ------------------------------
job=reseedmariabackup
LOG_DIR=/tmp/prlog; rm -rf "$LOG_DIR"; mkdir -p "$LOG_DIR"
# The real job writes the prepare output into the same log partialRestore uses.
cat /tmp/bk_prepare.log > "$LOG_DIR/reseed.out"
BACKUPDIR=/tmp/bk
DATADIR=/var/lib/mysql
USER=root PASSWORD=rootpw MYSQL_SERVER=127.0.0.1 MYSQL_PORT=3306
DB_CONN_PARAMETERS="-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT"
BINARY_CLIENT="mariadb $DB_CONN_PARAMETERS"
JOBS_MODE=api ID=""
PR_STATUS=0 PR_LOG=""
send_lines_to_api() { :; }
source /prtest/functions.sh
sc_before_restore "$SCENARIO"
sc_state >/tmp/pre.txt
[[ "$SCENARIO" == "interrupted" ]] && sc_interrupted_first_run
partialRestore; rc=$?

# ---- verdict -------------------------------------------------------------
checksums >/tmp/after.txt
echo "partialRestore rc=$rc"
echo "--- error summary (reseed.out)"
grep -E '^ERROR' "$LOG_DIR/reseed.out" | sed -E 's/[a-z_0-9]+\.[a-z_0-9#]+/X/g' | sort | uniq -c | sort -rn | head -12
echo "--- table checksums before-backup vs after-restore"
paste /tmp/before.txt /tmp/after.txt | awk '{s=($2==$4)?"OK":"MISMATCH"; print s, $1, $2, $4}'
echo "--- leftovers in datadir"
ls $DATADIR/prt_a/mrm_pivo* $DATADIR/prt_b/mrm_pivo* 2>/dev/null
for f in $DATADIR/prt_a/*.ibd $DATADIR/prt_b/*.ibd; do
    [ -e "$f" ] || continue; b=${f%.*}; t=${b%%#P#*}; [ -e "$t.frm" ] || echo "ORPHAN $f"
done
echo "--- integrity of non-table objects"
echo "view v_plain:      $(q "SELECT COUNT(*) FROM information_schema.views WHERE table_schema='prt_a' AND table_name='v_plain'")"
echo "trigger trg_plain: $(q "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema='prt_a' AND trigger_name='trg_plain'")"
echo "function f_double: $(q "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema='prt_a' AND routine_name='f_double'")"
echo "sequence seq1:     $(q "SELECT NEXTVAL(prt_a.seq1)" || echo MISSING) (backup had consumed 100)"
echo "aria_t rows:       $(q "SELECT COUNT(*) FROM prt_a.aria_t" || echo MISSING)"
echo "audit rows:        $(q "SELECT COUNT(*) FROM prt_a.audit" || echo MISSING)"
echo "foreign key:       $(q "SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema='prt_a' AND table_name='child'")"
echo "event ev_tick:     $(q "SELECT COUNT(*) FROM information_schema.events WHERE event_schema='prt_a' AND event_name='ev_tick'")"
echo "user bk_user:      $(q "SELECT COUNT(*) FROM mysql.user WHERE user='bk_user'")"
echo "extra db (not in backup) still present: $(q "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='prt_extra'")"
echo "--- stub/staging tables still registered"
q "SELECT CONCAT(table_schema,'.',table_name) FROM information_schema.tables WHERE table_name LIKE 'mrm_pivo%'"
echo "--- quarantine"
find $DATADIR/.system -path '*orphan-quarantine*' -type f 2>/dev/null
echo "--- skipped/reported"
grep -E 'SKIPPED|aborted|No login' "$LOG_DIR/reseed.out" | head -20
cp "$LOG_DIR/reseed.out" /prtest-out/reseed.out 2>/dev/null
sc_verdict "$SCENARIO" "$rc"
