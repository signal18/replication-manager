# Configurator: InnoDB buffer pool rounding (#1950)

The configurator sizes the engine buffers as a percentage of the usable memory (`prov-db-memory`, in MB) given by `prov-db-memory-shared-pct` (default `threads:10,innodb:45,myisam:4,aria:8,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,tidesdb:1,pfs:4,fscache:20`). Each enabled engine has a floor of 128 MB (`minEngineMemMB`), and a disabled one (0%) stays at 0.

## Rounding
- **Every engine buffer except the InnoDB buffer pool** (`engineMemMB`) is rounded down to a power of two (`largestPow2LE`), by design of the memory model.
- **The InnoDB buffer pool** (`innodbBufferPoolMB`, since #1950) is rounded down to a multiple of 128 MB, InnoDB's default `innodb_buffer_pool_chunk_size`, the unit it allocates and resizes in. With another chunk size or several buffer pool instances, InnoDB rounds the pool up to a multiple of chunk size × instances itself.

Why the buffer pool differs: a power-of-two round-down drops anywhere from 0 to almost half of the plan's share. With `innodb_flush_method = O_DIRECT`, InnoDB data never goes through the page cache, so the dropped memory did not serve InnoDB at all.

| Container | 45% | Power of two (before) | 128 MB multiple |
|---|---|---|---|
| 4 GB | 1843 | 1024 | 1792 |
| 8 GB | 3686 | 2048 | 3584 |
| 16 GB (4 DBU) | 7372 | 4096 | 7296 |
| 128 GB | 58982 | 32768 | 58880 |

## Page cache and redo log
The memory outside the buffers (the `fscache` share plus what the rounding leaves) keeps the **redo log** in the page cache, so partial-block redo writes never read from disk first. At 16 GB, with the default shares, about 5.6 GB is left against a 1 GB redo log. Follow-up #1951: never size `innodb_log_file_size` above that page cache.

Tests: `cluster/configurator/configurator_mem_test.go`.
