package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// Guards the container#db allocator environment (#1749): configurable jemalloc
// preload with MALLOC_ARENA_MAX derived from prov-cores; empty preload = off.
func TestOpenSVCGetDBContainerEnvironment(t *testing.T) {
	newServer := func(conf *config.Config) *ServerMonitor {
		c := new(Cluster)
		c.Conf = conf
		return &ServerMonitor{ClusterGroup: c}
	}

	t.Run("preload set derives arenas from prov-cores", func(t *testing.T) {
		s := newServer(&config.Config{ProvDBDockerJemallocPreload: "libjemalloc.so.2", ProvCores: "2"})
		env := s.OpenSVCGetDBContainerEnvironment()
		for _, want := range []string{"MYSQL_INITDB_SKIP_TZINFO=yes", "LD_PRELOAD=libjemalloc.so.2", "MALLOC_ARENA_MAX=2"} {
			if !strings.Contains(env, want) {
				t.Errorf("environment %q misses %q", env, want)
			}
		}
	})

	t.Run("preload value follows configuration verbatim", func(t *testing.T) {
		s := newServer(&config.Config{ProvDBDockerJemallocPreload: "/usr/lib/libjemalloc.so.1", ProvCores: "8"})
		env := s.OpenSVCGetDBContainerEnvironment()
		if !strings.Contains(env, "LD_PRELOAD=/usr/lib/libjemalloc.so.1") {
			t.Errorf("environment %q should carry the configured preload path", env)
		}
		if !strings.Contains(env, "MALLOC_ARENA_MAX=8") {
			t.Errorf("environment %q should derive MALLOC_ARENA_MAX=8 from prov-cores", env)
		}
	})

	t.Run("unparseable cores fall back to 2", func(t *testing.T) {
		s := newServer(&config.Config{ProvDBDockerJemallocPreload: "libjemalloc.so.2", ProvCores: ""})
		if env := s.OpenSVCGetDBContainerEnvironment(); !strings.Contains(env, "MALLOC_ARENA_MAX=2") {
			t.Errorf("environment %q should fall back to MALLOC_ARENA_MAX=2", env)
		}
	})

	t.Run("empty preload disables both exports", func(t *testing.T) {
		s := newServer(&config.Config{ProvDBDockerJemallocPreload: "", ProvCores: "2"})
		env := s.OpenSVCGetDBContainerEnvironment()
		if env != "MYSQL_INITDB_SKIP_TZINFO=yes" {
			t.Errorf("environment %q should only carry the base entry when disabled", env)
		}
	})
}
