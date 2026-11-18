package configurator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

// The Kubernetes database init container (PVC layout service-dirs-v1, #2395) installs the generated
// etc/mysql tree over /etc/mysql, which hides the image's own configuration, and refuses to let the
// database start unless that tree has a non-empty my.cnf and a conf.d with entries. This pins what the
// guard relies on, for the generator as it is, and that those files carry real configuration:
//   - conf.d holds at least the 10 unconditional links, every link one into ../replication-manager.d; every
//     entry (a link, or the plain file Galera adds) resolves, inside the generated tree, to a non-empty file;
//   - those files hold [mysqld] options, among them the datadir layout the Kubernetes mounts are built on;
//   - my.cnf is written only for a system.docker, system.pkg, system.rpm or system.deb tag, and when it is,
//     it includes conf.d.
//
// A Kubernetes cluster without any of these tags gets no my.cnf and the guard fails closed (#2395).
func TestGeneratedDBConfigSatisfiesKubernetesGuard(t *testing.T) {
	cases := []struct {
		name      string
		tags      []string
		wantMyCnf bool
	}{
		{"default tags", []string{"semisync", "row", "innodb", "noquerycache", "threadpool", "slow", "pfs", "docker", "linux", "readonly", "diskmonitor", "sqlerror", "compressbinlog"}, true},
		{"docker only", []string{"docker"}, true},
		{"mysql gtid", []string{"docker", "mysqlgtid", "innodb", "semisync"}, true},
		{"galera", []string{"docker", "wsrep", "wsrepgtid"}, true},
		{"no tag at all", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Configurator
			c.SetLogger(logrus.New())
			c.SetConfig(config.Config{ProvOrchestrator: config.ConstOrchestratorKubernetes, ProvDBConfig: true})
			if err := c.LoadDBModules(); err != nil {
				t.Fatal(err)
			}
			c.SetDBTags(tc.tags)
			datadir := t.TempDir()
			if err := c.GenerateDatabaseConfig(datadir, t.TempDir(), "/usr", map[string]string{}, "test", true); err != nil {
				t.Fatal(err)
			}

			etc := filepath.Join(datadir, "init/etc/mysql")
			confd := filepath.Join(etc, "conf.d")
			entries, err := os.ReadDir(confd)
			if err != nil {
				t.Fatal(err)
			}
			var options, links int
			for _, e := range entries {
				link := filepath.Join(confd, e.Name())
				if e.Type()&os.ModeSymlink != 0 {
					links++
					target, err := os.Readlink(link)
					if err != nil || !strings.HasPrefix(target, "../replication-manager.d/") {
						t.Errorf("conf.d/%s -> %q (%v), want a link into ../replication-manager.d/", e.Name(), target, err)
					}
				}
				resolved, err := filepath.EvalSymlinks(link)
				if err != nil {
					t.Errorf("conf.d/%s does not resolve: %v", e.Name(), err)
					continue
				}
				if rel, err := filepath.Rel(etc, resolved); err != nil || strings.HasPrefix(rel, "..") {
					t.Errorf("conf.d/%s resolves outside the generated tree: %s", e.Name(), resolved)
					continue
				}
				data, err := os.ReadFile(resolved)
				if err != nil || len(strings.TrimSpace(string(data))) == 0 {
					t.Errorf("conf.d/%s resolves to an empty or unreadable file (%v)", e.Name(), err)
					continue
				}
				options += countOptionLines(string(data))
			}
			if links < 10 {
				t.Errorf("conf.d has %d links (%d entries), want at least the 10 unconditional ones", links, len(entries))
			}
			if options == 0 {
				t.Error("no conf.d fragment carries an option")
			}

			// the datadir layout service-dirs-v1 mounts (/var/lib/mysql/.system) must be what the fragment says
			path, err := os.ReadFile(filepath.Join(confd, "01_default_path.cnf"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(path), "[mysqld]") || !strings.Contains(string(path), "innodb_data_home_dir") {
				t.Errorf("01_default_path.cnf carries no [mysqld] datadir options:\n%s", path)
			}
			if tc.wantMyCnf && !strings.Contains(string(path), "/var/lib/mysql/.system/innodb") {
				t.Errorf("01_default_path.cnf does not use the absolute /var/lib/mysql/.system paths:\n%s", path)
			}

			mycnf, err := os.ReadFile(filepath.Join(etc, "my.cnf"))
			switch {
			case tc.wantMyCnf && err != nil:
				t.Fatalf("my.cnf: %v", err)
			case tc.wantMyCnf:
				if !strings.Contains(string(mycnf), "[mysqld]") || !strings.Contains(string(mycnf), "!includedir /etc/mysql/conf.d") {
					t.Errorf("my.cnf does not include conf.d:\n%s", mycnf)
				}
			case err == nil:
				t.Errorf("my.cnf is written for tags %v, which the guard's documentation says it is not", tc.tags)
			}
		})
	}
}

// countOptionLines counts the lines that set an option: not blank, not a comment, not a section header.
func countOptionLines(content string) int {
	n := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "!") {
			continue
		}
		n++
	}
	return n
}
