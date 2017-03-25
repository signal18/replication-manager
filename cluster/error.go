package cluster

var clusterError = map[string]string{
	"ERR00005": "Error getting privileges for user %s@%s: %s",
	"ERR00006": "User must have REPLICATION CLIENT privilege",
	"ERR00007": "User must have REPLICATION SLAVE privilege",
	"ERR00008": "User must have SUPER privilege",
	"ERR00009": "User must have RELOAD privilege",
	"ERR00011": "Multiple masters detected but explicity setup, setting the parameter",
	"ERR00013": "Binary log disabled on slave: %s",
	"ERR00014": "Error getting binlog dump count on server %s: %s",
	"ERR00015": "Error getting privileges for user %s on server %s: %s",
	"ERR00016": "Network issue - Master is unreachable but slaves are replicating",
	"ERR00017": "MaxScale no monitor capture",
}
