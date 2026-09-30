#!/bin/bash
# Usage: lab_dbstate.sh <db1|db2>  -> per-table row count + content hash and object counts (copy to opensvc-node1 as /tmp/dbstate.sh)
H="$1.repmanlab.svc.repman-lab"
U=$(sudo python3 -c 'import re;t=open("/etc/replication-manager/cluster.d/repmanlab.toml").read();print(re.search(r"^\s*db-servers-credential\s*=\s*\"([^\"]*)\"",t,re.M).group(1))')
CNF=$(mktemp); chmod 600 "$CNF"; printf "[client]\nuser=%s\npassword=%s\n" "${U%%:*}" "${U#*:}" > "$CNF"
M() { sudo docker exec -i repmanlab..db1.container.db mariadb --defaults-extra-file=/dev/stdin -h "$H" -N -B -e "$1" < "$CNF"; }
SYS="('mysql','sys','performance_schema','information_schema','replication_manager_schema')"
M "SELECT table_schema, table_name FROM information_schema.tables WHERE table_type='BASE TABLE' AND table_schema NOT IN $SYS ORDER BY 1,2" |
while IFS=$'\t' read -r d n; do
    # Content hash (all rows, sorted) + row count: CHECKSUM TABLE is not stable
    # for tables with STORED generated columns (its value changes when the
    # table is reopened, with no data change).
    printf 'T %s.%s rows=%s md5=%s\n' "$d" "$n" "$(M "SELECT COUNT(*) FROM \`$d\`.\`$n\`")" "$(M "SELECT * FROM \`$d\`.\`$n\`" | LC_ALL=C sort | md5sum | cut -c1-32)"
done
M "SELECT CONCAT('C views=',(SELECT COUNT(*) FROM information_schema.views WHERE table_schema NOT IN $SYS),' routines=',(SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema NOT IN $SYS),' triggers=',(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema NOT IN $SYS),' events=',(SELECT COUNT(*) FROM information_schema.events WHERE event_schema NOT IN $SYS),' fks=',(SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema NOT IN $SYS),' partitioned=',(SELECT COUNT(DISTINCT table_schema,table_name) FROM information_schema.partitions WHERE partition_name IS NOT NULL AND table_schema NOT IN $SYS))"
M "SELECT CONCAT('G gtid=',@@gtid_current_pos)"
rm -f "$CNF"
