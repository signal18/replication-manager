#!/bin/bash
version=$(git describe --tags)
echo  "{\"Build\"=\"$version\", \"setups\"=[" > result-$version.json
for i in $(find . -name "*.conf") ; do
  echo "{\"name\"=\"$i\", \"results\"=[" >> result-$version.json
  testdir=$(dirname "${i}")
  echo $testdir
  tests=`cat $testdir/tests.todo`
  for test in $tests ; do
   ../../replication-manager-pro --config=./$i monitor  &
   pid="$!"
   sleep 3
   while [[ $(../../replication-manager-pro api --url=https://127.0.0.1:3000/api/status) != "{\"alive\": \"running\"}" ]] ; do
    echo "waiting start service"
    sleep 1
   done
    ../../replication-manager-pro test --run-tests="$test" >> result-$version.json
   kill $pid
   echo ","  >> result-$version.json

  done
  echo "]},"  >> result-$version.json


done
echo "]}" >> result-$version.json
