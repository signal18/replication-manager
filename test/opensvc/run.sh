#!/bin/bash
version=$(git describe --tags)
for i in $(find ./$1 -name "*.conf") ; do
  testdir=$(dirname "${i}")
  echo  "{\"Build\":\"$version\", \"setups\":[" > $testdir/result-$version.json
  echo "{\"name\":\"$i\", \"results\":[" >> $testdir/result-$version.json
  echo $testdir
  tests=`cat $testdir/tests.todo`
  for test in $tests ; do
   > $testdir/$test.log
   ../../replication-manager-pro --test --logfile=$testdir/$test.log --config=./$i monitor  &
   pid="$!"
   sleep 3
   while [[ $(../../replication-manager-pro api --url=https://127.0.0.1:3000/api/status) != "{\"alive\": \"running\"}" ]] ; do
    echo "waiting start service"
    sleep 1
   done
    ../../replication-manager-pro test --run-tests="$test" >> $testdir/result-$version.json
   kill $pid
   echo ","  >> $testdir/result-$version.json

  done
  echo "]},"  >> $testdir/result-$version.json
  echo "]}" >> $testdir/result-$version.json

done
