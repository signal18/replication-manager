#!/bin/bash
USER=root
PASSWORD=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
ERROLOG=/var/lib/mysql/.system/logs/errors.log
SLOWLOG=/var/lib/mysql/.system/logs/sql-slow
BACKUPDIR=/var/lib/mysql/.system/backup
DATADIR=/var/lib/mysql/
JOBS=( "xtrabackup" "error" "slowquery" "zfssnapback" "optimize" "reseedxtrabackup" "reseedmysqldump" "flashbackxtrabackup" "flashbackmysqldump" )

doneJob()
{
 /usr/bin/mysql -u$USER -p$PASSWORD -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set end=NOW(), result=LOAD_FILE('/tmp/dbjob.out') WHERE id='$ID';" &
}

pauseJob()
{
 /usr/bin/mysql -u$USER -p$PASSWORD -e "select sleep(6);set sql_log_bin=0;UPDATE replication_manager_schema.jobs set result=LOAD_FILE('/tmp/dbjob.out') WHERE id='$ID';" &
}

partialRestore()
{
 /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;install plugin BLACKHOLE soname 'ha_blackhole.so'"
 for dir in $(ls -d $BACKUPDIR/*/ | xargs -n 1 basename | grep -vE 'mysql|performance_schema|replication_manager_schema') ; do
 /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;drop database IF EXISTS $dir; CREATE DATABASE $dir;"
 chown -R mysql:mysql $BACKUPDIR

  for file in $(find $BACKUPDIR/$dir/ -name "*.exp" | xargs -n 1 basename | cut -d'.' --complement -f2-) ; do
   cat $BACKUPDIR/$dir/$file.frm | sed -e 's/\x06\x00\x49\x6E\x6E\x6F\x44\x42\x00\x00\x00/\x09\x00\x42\x4C\x41\x43\x4B\x48\x4F\x4C\x45/g' > $DATADIR/$dir/mrm_pivo.frm
   chown mysql:mysql $DATADIR/$dir/mrm_pivo.frm
   /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;ALTER TABLE $dir.mrm_pivo  engine=innodb;RENAME TABLE $dir.mrm_pivo TO $dir.$file; ALTER TABLE $dir.$file DISCARD TABLESPACE;"
   mv $BACKUPDIR/$dir/$file.ibd $DATADIR/$dir/$file.ibd
   mv $BACKUPDIR/$dir/$file.exp $DATADIR/$dir/$file.exp
   mv $BACKUPDIR/$dir/$file.cfg $DATADIR/$dir/$file.cfg
   mv $BACKUPDIR/$dir/$file.TRG $DATADIR/$dir/$file.TRG
   /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;ALTER TABLE $dir.$file IMPORT TABLESPACE"
  done
  for file in $(find $BACKUPDIR/$dir/ -name "*.MYD" | xargs -n 1 basename | cut -d'.' --complement -f2-) ; do
   mv $BACKUPDIR/$dir/$file.* $DATADIR/$dir/
   /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;FLUSH TABLE $dir.$file"
  done
  for file in $(find $BACKUPDIR/$dir/ -name "*.CSV" | xargs -n 1 basename | cut -d'.' --complement -f2-) ; do
   mv $BACKUPDIR/$dir/$file.* $DATADIR/$dir/
   /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;FLUSH TABLE $dir.$file"
  done
 done
 for file in $(find $BACKUPDIR/mysql/ -name "*.MYD" | xargs -n 1 basename | cut -d'.' --complement -f2-) ; do
   mv $BACKUPDIR/mysql/$file.* $DATADIR/mysql/
   /usr/bin/mysql -p$PASSWORD -u$USER -e "set sql_log_bin=0;FLUSH TABLE mysql.$file"
 done
 cat $BACKUPDIR/xtrabackup_info | grep binlog_pos | awk  -F, '{ print $3 }' | sed -e 's/GTID of the last change/set sql_log_bin=0;set global gtid_slave_pos=/g' | /usr/bin/mysql -p$PASSWORD -u$USER
 /usr/bin/mysql -p$PASSWORD -u$USER  -e"flush privileges;start slave;"
}

for job in "${JOBS[@]}"
do

 TASK=($(echo "select concat(id,'@',server,':',port) from replication_manager_schema.jobs WHERE task='$job' and done=0 order by task desc limit 1" | /usr/bin/mysql -p$PASSWORD -u$USER -N))

 ADDRESS=($(echo $TASK | awk -F@ '{ print $2 }'))
 ID=($(echo $TASK | awk -F@ '{ print $1 }'))
 /usr/bin/mysql -uroot -p$PASSWORD -e "set sql_log_bin=0;UPDATE replication_manager_schema.jobs set done=1 WHERE task='$job';"

  if [ "$ADDRESS" == "" ]; then
    echo "No $job needed"
  else
    echo "Processing $job"
    case "$job" in
      reseedmysqldump)
       echo "Waiting backup." >  /tmp/dbjob.out
       pauseJob
       socat -u TCP-LISTEN:4444,reuseaddr STDOUT | gunzip | /usr/bin/mysql -p$PASSWORD -u$USER > /tmp/dbjob.out 2>&1
        /usr/bin/mysql -p$PASSWORD -u$USER -e 'start slave;'
      ;;
      flashbackmysqldump)
       echo "Waiting backup." >  /tmp/dbjob.out
       pauseJob
       socat -u TCP-LISTEN:4444,reuseaddr STDOUT | gunzip | /usr/bin/mysql -p$PASSWORD -u$USER > /tmp/dbjob.out 2>&1
        /usr/bin/mysql -p$PASSWORD -u$USER -e 'start slave;'
      ;;
      reseedxtrabackup)
       rm -rf $BACKUPDIR
       mkdir $BACKUPDIR
       echo "Waiting backup." >  /tmp/dbjob.out
       pauseJob
       socat -u TCP-LISTEN:4444,reuseaddr STDOUT | xbstream -x -C $BACKUPDIR
       xtrabackup --prepare --export --target-dir=$BACKUPDIR
       partialRestore
      ;;
      flashbackxtrabackup)
       rm -rf $BACKUPDIR
       mkdir $BACKUPDIR
       echo "Waiting backup." >  /tmp/dbjob.out
       pauseJob
       socat -u TCP-LISTEN:4444,reuseaddr STDOUT | xbstream -x -C $BACKUPDIR
       xtrabackup --prepare --export --target-dir=$BACKUPDIR
       partialRestore
      ;;
      xtrabackup)
       cd /docker-entrypoint-initdb.d
       /usr/bin/innobackupex  --defaults-file=/etc/mysql/my.cnf --socket='/var/run/mysqld/mysqld.sock' --slave-info --no-version-check  --user=$USER --password=$PASSWORD --stream=xbstream /tmp/ | socat -u stdio TCP:$ADDRESS &>/tmp/dbjob.out
      ;;
      error)
       cat $ERROLOG| socat -u stdio TCP:$ADDRESS &>/tmp/dbjob.out
       > $ERROLOG
      ;;
      slowquery)
       cat $SLOWLOG| socat -u stdio TCP:$ADDRESS &>/tmp/dbjob.out
       > $SLOWLOG
      ;;
      zfssnapback)
       LASTSNAP=`zfs list -r -t all |grep zp%%ENV:SERVICES_SVCNAME%%_pod01 | grep daily | sort -r | head -n 1  | cut -d" " -f1`
       %%ENV:SERVICES_SVCNAME%% stop
       zfs rollback $LASTSNAP
       %%ENV:SERVICES_SVCNAME%% start
      ;;
      optimize)
       /usr/bin/mysqloptimize -u$USER -p$PASSWORD --all-databases &>/tmp/dbjob.out
      ;;
  esac
  doneJob
  fi

done
