package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppConfiguratorSizing(t *testing.T) {
	env := appConfiguratorSizing(4096, 2, 0)
	for token, want := range map[string]string{
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_SIZE%%": "1024", // a quarter of the memory
		"%%ENV:SVC_CONF_ENV_MAX_CONNECTIONS%%":         "100",
		"%%ENV:SVC_CONF_ENV_JOIN_BUFFER_SIZE%%":        "10",  // a quarter shared by the sessions
		"%%ENV:SVC_CONF_ENV_MAX_SESSION_MEM_USED%%":    "256", // a sixteenth
		"%%ENV:SVC_CONF_ENV_INNODB_PURGE_THREADS%%":    "2",
		"%%ENV:CHECKPOINTIOPS%%":                       "200", // no declared iops
	} {
		if env[token] != want {
			t.Fatalf("%s = %s, want %s", token, env[token], want)
		}
	}
	small := appConfiguratorSizing(512, 0, 0)
	if small["%%ENV:SVC_CONF_ENV_MAX_CONNECTIONS%%"] != "50" || small["%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_SIZE%%"] != "128" || small["%%ENV:SVC_CONF_ENV_INNODB_PURGE_THREADS%%"] != "1" {
		t.Fatalf("small plan: %v", small)
	}
}

// The embedded PostgreSQL moduleset renders for an app: every token it uses is provided,
// the files land on absolute paths, and the script writes them byte for byte.
func TestAppConfiguratorPostgresModuleset(t *testing.T) {
	module, err := loadAppConfiguratorModule("postgres")
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderAppConfiguratorFiles(module, "postgres", appConfiguratorSizing(2048, 2, 1000))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "/etc/postgresql/replication-manager.d/01_memory.conf" || files[1].Path != "/etc/postgresql/replication-manager.d/02_vacuum.conf" {
		t.Fatalf("files: %+v", files)
	}
	for _, want := range []string{"shared_buffers = 512MB", "max_connections = 100", "maintenance_work_mem = 128MB"} {
		if !strings.Contains(files[0].Content, want) {
			t.Fatalf("memory file misses %q:\n%s", want, files[0].Content)
		}
	}
	for _, want := range []string{"autovacuum_max_workers = 2", "autovacuum_vacuum_cost_limit = 1000"} {
		if !strings.Contains(files[1].Content, want) {
			t.Fatalf("vacuum file misses %q:\n%s", want, files[1].Content)
		}
	}
	for _, f := range files {
		if strings.Contains(f.Content, "%%") {
			t.Fatalf("unresolved token in %s:\n%s", f.Path, f.Content)
		}
	}

	// the script, run by a real shell under a scratch root, writes the same bytes
	root := t.TempDir()
	for i := range files {
		files[i].Path = filepath.Join(root, files[i].Path)
	}
	cmd := exec.Command("sh")
	cmd.Stdin = strings.NewReader(appConfiguratorScript(files))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v %s", err, out)
	}
	for _, f := range files {
		got, err := os.ReadFile(f.Path)
		want := f.Content
		if !strings.HasSuffix(want, "\n") {
			want += "\n"
		}
		if err != nil || string(got) != want {
			t.Fatalf("%s: %v\n%q\nwant\n%q", f.Path, err, got, want)
		}
	}
}

func TestAppConfiguratorRefusals(t *testing.T) {
	if _, err := loadAppConfiguratorModule("../etc"); err == nil {
		t.Fatal("an engine name is a plain word")
	}
	if _, err := loadAppConfiguratorModule("nosuchengine"); err == nil {
		t.Fatal("an engine without a moduleset is refused")
	}
	module, _ := loadAppConfiguratorModule("postgres")
	if _, err := renderAppConfiguratorFiles(module, "postgres", map[string]string{}); err == nil || !strings.Contains(err.Error(), "SVC_CONF_ENV_MAX_CONNECTIONS") {
		t.Fatalf("a token the app does not provide refuses the render, naming it: %v", err)
	}
}
