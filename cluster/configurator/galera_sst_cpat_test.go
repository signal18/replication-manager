package configurator

import (
	"os"
	"strings"
	"testing"
)

func TestGaleraSplitPathSSTCpat(t *testing.T) {
	if !(&Configurator{DBTags: []string{"innodb", "wsrep"}}).galeraSplitPathSST() {
		t.Fatal("Galera on the split path layout needs the [sst] cpat")
	}
	if !(&Configurator{DBTags: []string{"wsrep", "nosplitpath"}}).galeraSplitPathSST() {
		t.Fatal("nosplitpath keeps the .system mount points: the cpat is needed too")
	}
	if (&Configurator{DBTags: []string{"innodb"}}).galeraSplitPathSST() {
		t.Fatal("not Galera: no cpat")
	}
	dir := t.TempDir()
	if err := (&Configurator{}).writeGaleraSSTCpat(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dir + "/init/etc/mysql/conf.d/" + galeraSSTCpatFile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// the stock patterns are kept (a cpat replaces the default, it does not extend it)
	for _, want := range []string{"[sst]\n", `.*galera\.cache$`, `.*grastate\.dat$`, `.*\.sst$`, `\|.*/\.system$`, `\|.*/\.system/innodb/redo$`} {
		if !strings.Contains(s, want) {
			t.Errorf("%q missing:\n%s", want, s)
		}
	}
}

func TestGaleraISTRecvBind(t *testing.T) {
	in := "[mysqld]\nwsrep_node_address=db3.x\nwsrep_provider_options=\"gcache.size=128M; gmcast.segment=0\"\n#wsrep_provider_options=\"evs.keepalive_period = PT2S\"\n"
	out := galeraISTRecvBind(in)
	if !strings.Contains(out, `wsrep_provider_options="gcache.size=128M; gmcast.segment=0; ist.recv_bind=0.0.0.0"`) {
		t.Fatalf("recv_bind not appended:\n%s", out)
	}
	if !strings.Contains(out, `#wsrep_provider_options="evs.keepalive_period = PT2S"`) {
		t.Fatalf("a commented line must stay as it is:\n%s", out)
	}
	if again := galeraISTRecvBind(out); again != out {
		t.Fatalf("idempotent: an existing ist.recv_bind is kept:\n%s", again)
	}
	if out := galeraISTRecvBind("[mysqld]\nwsrep_provider_options=\"\"\n"); !strings.Contains(out, `wsrep_provider_options="ist.recv_bind=0.0.0.0"`) {
		t.Fatalf("empty options: %s", out)
	}
}
