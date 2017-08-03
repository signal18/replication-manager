#!/bin/bash
version=$(git describe --tags)
for i in $(find ./$1 -name "*.conf") ; do
  testdir=$(dirname "${i}")
  destdir=$testdir/$version
  mkdir $destdir

  echo "{\"results\":[" >> $destdir/result.json
  echo $testdir
  tests=`cat $testdir/tests.todo`
  for test in $tests ; do
   > $testdir/$test.log
   ../../replication-manager-pro --test --logfile=$destdir/$test.log --config=./$i monitor  &
   pid="$!"
   sleep 3
   while [[ $(../../replication-manager-pro api --url=https://127.0.0.1:3000/api/status) != "{\"alive\": \"running\"}" ]] ; do
    echo "waiting start service"
    sleep 1
   done
    ../../replication-manager-pro test --run-tests="$test" >> $testdir/result.json
   kill $pid
   echo ","  >> $destdir/result.json

  done
  echo "]},"  >> $destdir/result.json


done
