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
