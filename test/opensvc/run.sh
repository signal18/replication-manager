#!/bin/bash
version=$(git describe --tags)

for i in $(find ./$1 -name "*.conf") ; do
  testdir=$(dirname "${i}")
  destdir=$testdir/$version
  mkdir $destdir
  echo $testdir
  > $destdir/result.json
  echo "{\"results\":[" >> $destdir/result.json

  tests=`cat $testdir/tests.todo`
  COUNTER=0
  lasttest=`cat $testdir/tests.todo| wc -l`

  for test in $tests ; do
   > $desdir/$test.log
   ../../replication-manager-pro --test --logfile=$destdir/$test.log --config=./$i monitor  &
   pid="$!"
   sleep 8
   while [[ $(../../replication-manager-pro api --url=https://127.0.0.1:3000/api/status) != "{\"alive\": \"running\"}" ]] ; do
    echo "waiting start service"
    sleep 1
   done
   res=$(../../replication-manager-pro test --run-tests="$test")
   echo $res  >> $destdir/result.json
   kill $pid
   $((COUNTER++))
   if [[ "$COUNTER" -ne "$lasttest" ]]; then
      echo ","  >> $destdir/result.json
   fi
  done
  echo "]}"  >> $destdir/result.json
  # Convert result to html
   ../../replication-manager-pro test --convert --file="$destdir/result.json" > $destdir/result.html
done
tree config -P result.html -H "http://htmlpreview.github.io/?https://github.com/tanji/replication-manager/tree/develop/test/opensvc/config"  > ../../doc/regtest.html
