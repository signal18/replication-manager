package metrics

import (
	"testing"
)

const (
	METRICS_JSON = "../test/test_metrics.json"
)

func BenchmarkMetrics_ParseMetrics(b *testing.B) {

	wantedMetrics := []string{"scur", "qcur", "qmax", "smax", "slim", "econ,", "status", "lastsess", "qtime", "ctime", "rtime", "ttime", "req_rate", "req_rate_max", "req_tot", "rate", "rate_lim", "rate_max", "hrsp_1xx", "hrsp_2xx", "hrsp_3xx", "hrsp_4xx", "hrsp_5xx"}
	m := make(map[chan Metric]bool)
	c := make(chan Metric)
	m[c] = true

	testdata := getMapofMetrics()
	statsChannel := make(chan map[string]map[string]string)

	for n := 0; n < b.N; n++ {
		go ParseMetrics(statsChannel, m, wantedMetrics)
		statsChannel <- testdata
	}
}

func TestMetrics_ParseMetrics(t *testing.T) {

	wantedMetrics := []string{"scur", "qcur", "qmax", "smax", "slim", "econ,", "status", "lastsess", "qtime", "ctime", "rtime", "ttime", "req_rate", "req_rate_max", "req_tot", "rate", "rate_lim", "rate_max", "hrsp_1xx", "hrsp_2xx", "hrsp_3xx", "hrsp_4xx", "hrsp_5xx"}
	m := make(map[chan Metric]bool)
	c := make(chan Metric)
	m[c] = true

	testdata := getMapofMetrics()
	statsChannel := make(chan map[string]map[string]string)

	go ParseMetrics(statsChannel, m, wantedMetrics)

	statsChannel <- testdata

	for s, _ := range m {
		metric := <-s
		if metric.Value != 0 {
			t.Errorf("value was %d", metric.Value)
		}
	}
}

func getMapofMetrics() map[string]map[string]string {

	//prepare test data
	l := map[string]string{"pxname": "test_route_2.service_b", "svname": "paas.fb76ea52-098f-4e2a-abbe-0238c3d48480", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "1000", "stot": "0", "bin": "0", "bout": "0", "dreq": "", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "no check", "weight": "100", "act": "1", "bck": "0", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "8", "sid": "1", "throttle": "", "lbtot": "0", "tracked": "", "type": "2", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "0", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "", "comp_out": "", "comp_byp": "", "comp_rsp": "", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0"}
	o := map[string]string{"pxname": "test_route_2.service_b", "svname": "BACKEND", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "100000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "UP", "weight": "100", "act": "1", "bck": "0", "chkfail": "", "chkdown": "0", "lastchg": "141", "downtime": "0", "qlimit": "", "pid": "1", "iid": "8", "sid": "0", "throttle": "", "lbtot": "0", "tracked": "", "type": "1", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	p := map[string]string{"pxname": "abusers", "svname": "BACKEND", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "1", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "UP", "weight": "0", "act": "0", "bck": "0", "chkfail": "", "chkdown": "0", "lastchg": "141", "downtime": "0", "qlimit": "", "pid": "1", "iid": "9", "sid": "0", "throttle": "", "lbtot": "0", "tracked": "", "type": "1", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	q := map[string]string{"pxname": "vamp_route_1", "svname": "FRONTEND", "qcur": "", "qmax": "", "scur": "0", "smax": "0", "slim": "500000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "0", "econ": "", "eresp": "", "wretr": "", "wredis": "", "status": "OPEN", "weight": "", "act": "", "bck": "", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "2", "sid": "0", "throttle": "", "lbtot": "", "tracked": "", "type": "0", "rate": "0", "rate_lim": "0", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "0", "req_rate_max": "0", "req_tot": "0", "cli_abrt": "", "srv_abrt": "", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "", "last_chk": "", "last_agt": "", "qtime": "", "ctime": "", "rtime": "", "ttime": "", "": ""}
	r := map[string]string{"pxname": "vamp_route_2", "svname": "BACKEND", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "50000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "UP", "weight": "0", "act": "0", "bck": "0", "chkfail": "", "chkdown": "0", "lastchg": "141", "downtime": "0", "qlimit": "", "pid": "1", "iid": "2", "sid": "0", "throttle": "", "lbtot": "0", "tracked": "", "type": "1", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	s := map[string]string{"pxname": "test_route_2", "svname": "FRONTEND", "qcur": "", "qmax": "", "scur": "0", "smax": "0", "slim": "500000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "0", "econ": "", "eresp": "", "wretr": "", "wredis": "", "status": "OPEN", "weight": "", "act": "", "bck": "", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "3", "sid": "0", "throttle": "", "lbtot": "", "tracked": "", "type": "0", "rate": "0", "rate_lim": "0", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "0", "req_rate_max": "0", "req_tot": "0", "cli_abrt": "", "srv_abrt": "", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "", "last_chk": "", "last_agt": "", "qtime": "", "ctime": "", "rtime": "", "ttime": "", "": ""}
	t := map[string]string{"pxname": "test_route_2.service_a", "svname": "FRONTEND", "qcur": "", "qmax": "", "scur": "0", "smax": "0", "slim": "500000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "0", "econ": "", "eresp": "", "wretr": "", "wredis": "", "status": "OPEN", "weight": "", "act": "", "bck": "", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "4", "sid": "0", "throttle": "", "lbtot": "", "tracked": "", "type": "0", "rate": "0", "rate_lim": "0", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "0", "req_rate_max": "0", "req_tot": "0", "cli_abrt": "", "srv_abrt": "", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "", "last_chk": "", "last_agt": "", "qtime": "", "ctime": "", "rtime": "", "ttime": "", "": ""}
	u := map[string]string{"pxname": "test_route_2.service_b", "svname": "FRONTEND", "qcur": "", "qmax": "", "scur": "0", "smax": "0", "slim": "500000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "0", "econ": "", "eresp": "", "wretr": "", "wredis": "", "status": "OPEN", "weight": "", "act": "", "bck": "", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "5", "sid": "0", "throttle": "", "lbtot": "", "tracked": "", "type": "0", "rate": "0", "rate_lim": "0", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "0", "req_rate_max": "0", "req_tot": "0", "cli_abrt": "", "srv_abrt": "", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "", "last_chk": "", "last_agt": "", "qtime": "", "ctime": "", "rtime": "", "ttime": "", "": ""}
	v := map[string]string{"pxname": "test_route_2", "svname": "test_route_2.service_a", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "", "stot": "0", "bin": "0", "bout": "0", "dreq": "", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "no check", "weight": "30", "act": "1", "bck": "0", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "6", "sid": "1", "throttle": "", "lbtot": "0", "tracked": "", "type": "2", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "0", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "", "comp_out": "", "comp_byp": "", "comp_rsp": "", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	w := map[string]string{"pxname": "test_route_2", "svname": "test_route_2.service_b", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "", "stot": "0", "bin": "0", "bout": "0", "dreq": "", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "no check", "weight": "70", "act": "1", "bck": "0", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "6", "sid": "2", "throttle": "", "lbtot": "0", "tracked": "", "type": "2", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "0", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "", "comp_out": "", "comp_byp": "", "comp_rsp": "", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	x := map[string]string{"pxname": "test_route_2", "svname": "BACKEND", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "50000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "UP", "weight": "100", "act": "2", "bck": "0", "chkfail": "", "chkdown": "0", "lastchg": "141", "downtime": "0", "qlimit": "", "pid": "1", "iid": "6", "sid": "0", "throttle": "", "lbtot": "0", "tracked": "", "type": "1", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	y := map[string]string{"pxname": "test_route_2.service_a", "svname": "paas.55f73f0d-6087-4964-a70e-b1ca1d5b24cd", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "1000", "stot": "0", "bin": "0", "bout": "0", "dreq": "", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "no check", "weight": "100", "act": "1", "bck": "0", "chkfail": "", "chkdown": "", "lastchg": "", "downtime": "", "qlimit": "", "pid": "1", "iid": "7", "sid": "1", "throttle": "", "lbtot": "0", "tracked": "", "type": "2", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "0", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "", "comp_out": "", "comp_byp": "", "comp_rsp": "", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}
	z := map[string]string{"pxname": "test_route_2.service_a", "svname": "BACKEND", "qcur": "0", "qmax": "0", "scur": "0", "smax": "0", "slim": "50000", "stot": "0", "bin": "0", "bout": "0", "dreq": "0", "dresp": "0", "ereq": "", "econ": "0", "eresp": "0", "wretr": "0", "wredis": "0", "status": "UP", "weight": "100", "act": "1", "bck": "0", "chkfail": "", "chkdown": "0", "lastchg": "141", "downtime": "0", "qlimit": "", "pid": "1", "iid": "7", "sid": "0", "throttle": "", "lbtot": "0", "tracked": "", "type": "1", "rate": "0", "rate_lim": "", "rate_max": "0", "check_status": "", "check_code": "", "check_duration": "", "hrsp_1xx": "0", "hrsp_2xx": "0", "hrsp_3xx": "0", "hrsp_4xx": "0", "hrsp_5xx": "0", "hrsp_other": "0", "hanafail": "", "req_rate": "", "req_rate_max": "", "req_tot": "", "cli_abrt": "0", "srv_abrt": "0", "comp_in": "0", "comp_out": "0", "comp_byp": "0", "comp_rsp": "0", "lastsess": "-1", "last_chk": "", "last_agt": "", "qtime": "0", "ctime": "0", "rtime": "0", "ttime": "0", "": ""}

	mapOfMaps := make(map[string]map[string]string)

	mapOfMaps[l["pxname"]] = l
	mapOfMaps[o["pxname"]] = o
	mapOfMaps[p["pxname"]] = p
	mapOfMaps[q["pxname"]] = q
	mapOfMaps[r["pxname"]] = r
	mapOfMaps[s["pxname"]] = s
	mapOfMaps[t["pxname"]] = t
	mapOfMaps[u["pxname"]] = u
	mapOfMaps[v["pxname"]] = v
	mapOfMaps[w["pxname"]] = w
	mapOfMaps[x["pxname"]] = x
	mapOfMaps[y["pxname"]] = y
	mapOfMaps[z["pxname"]] = z

	return mapOfMaps

}
