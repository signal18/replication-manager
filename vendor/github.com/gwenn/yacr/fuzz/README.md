[go-fuzz](https://github.com/dvyukov/go-fuzz) for yacr

```sh
go-fuzz-build github.com/gwenn/yacr/fuzz
go-fuzz -bin=./csv-fuzz.zip -workdir=.
```

```
2015/07/29 18:06:16 slaves: 4, corpus: 76 (32s ago), crashers: 0, restarts: 1/9061, execs: 588998 (9813/sec), cover: 295, uptime: 1m0s
```