# peer.json decoding and the peer client (#1948, #1953)

Two failures on 2026-10-09 emptied preprod's peer view. Both are fixed on the replication-manager side, and the first one also in the BO.

## 1. Sizes with a unit in peer.json (#1948)
The BO builds `peer.json` from harvested config values (`peer4domain`, dbaas-portal `scripts/create-peer-function.sql`). `prov-db-memory` and `prov-db-disk-size` may carry a unit in the config (`"4G"`, the flag default, or `"20G"`), while `PeerCluster` read them as integers (`,string`). The file was decoded as a whole, so one such entry made `json.Unmarshal` fail and emptied the whole peer list.

**replication-manager (`peer/peer_decode.go`):**
- `PeerCluster.UnmarshalJSON` reads memory (MB) and disk (GB) with the configurator's own parser, `config.ParseUnitMeasurementToInt` with `M,bytes,required` / `G,bytes,required`. Peer reading therefore accepts exactly what the config accepts. Cores and IOPS are counts, quoted or not. Negative values are refused, and empty or null is 0. The `,string` tags on the struct only describe how the fields are written.
- `DecodePeerList` decodes entry by entry. An unreadable entry is skipped and logged with its domain and cluster. A file that is not a list is still an error.
- A file with a skipped entry is applied without removing anything (`BatchUpdateClusters(…, removeOld=false)`). A cluster known from the previous file is never dropped because of a bad entry.

**BO (dbaas-portal 310cfe0):** `peer_size_value(variable, value)` writes memory in bare MB and disk in bare GB, so every replication-manager version, including older ones, reads `peer.json`. The same change exports `prov-db-cpu-cores`, which a trailing space in the variable list had kept out.

## 2. Peer requests with a leading double slash (#1953)
`PeerClient.DoRequest` joined `baseURL + "/" + "/api/health"`, which sends `//api/health`. Since 96678a8ad (2026-10-01) both routers set `SkipClean(true)`, so that a setting value in the path can start with a slash. On a recent peer, `//api/health` matched nothing and its `NotFoundHandler` redirected it to `/`, the dashboard page (HTML, 200): "failed to parse health status: invalid character '<'".

- **Server (`server/http_leading_slash.go`):** `redispatchLeadingSlashes`, called first in the `NotFoundHandler` of the API and HTTP routers, serves a path starting with several slashes as the same path with one leading slash. That covers every client already deployed. Only leading slashes are collapsed; `set/name//tmp/x.sh` keeps its value.
- **Client:** one slash between the base URL and the endpoint.
