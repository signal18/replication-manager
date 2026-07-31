package cluster

import (
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/tidwall/gjson"
)

// newSubstitutionTestCluster builds a minimal cluster with one server and two
// apps: one with a normal route deployment, one with a nil Routes slice (to
// verify buildAppSubstitutionView preserves the nil-vs-empty-slice
// distinction that config.Routes(nil) has in the original sheriff-driven
// output).
func newSubstitutionTestCluster(t *testing.T) (cluster *Cluster, appWithRoutes *App, appNoRoutes *App) {
	t.Helper()
	cluster = &Cluster{
		Name: "pfx-cluster",
		Conf: &config.Config{Timeout: 5},
	}
	cluster.Servers = serverList{
		&ServerMonitor{Id: "1", Host: "10.0.0.1", Port: "3306", ClusterGroup: cluster},
	}
	cluster.Proxies = []DatabaseProxy{
		&HaproxyProxy{Proxy: Proxy{Id: "prx1", Name: "prx1", Type: "haproxy", Host: "10.0.0.5", Port: "3307", Version: "1.0.0", ClusterGroup: cluster}},
	}

	appWithRoutes = &App{
		Id: "app1", Name: "app1", Type: "web", Host: "app1.internal", Port: "80", Version: "1.2.3",
		Mutex: &sync.Mutex{},
		AppConfig: &config.AppConfig{
			AppDbUser:   "dbuser",
			AppDbPass:   "dbpass",
			AppDbSchema: "dbschema",
			Deployment: &config.Deployment{
				Routes: config.Routes{
					{CName: "app1.example.com", Protocol: "https", Primary: true},
					{CName: "app1-admin.example.com", Protocol: "https"},
				},
			},
		},
		ClusterGroup: cluster,
	}
	appWithRoutes.CheckPrimaryRoute()

	appNoRoutes = &App{
		Id: "app2", Name: "app2", Type: "web", Host: "app2.internal", Port: "8080", Version: "0.1.0",
		Mutex: &sync.Mutex{},
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{}, // Routes intentionally left nil
		},
		ClusterGroup: cluster,
	}

	cluster.Apps = []*App{appWithRoutes, appNoRoutes}
	return cluster, appWithRoutes, appNoRoutes
}

// TestGetAppsSubstitutionJSon_ShapePreserved locks down the JSON shape that
// GetAppsSubstitutionJSon must keep producing after replacing the
// sheriff-over-live-App marshal with buildAppSubstitutionView's independent
// copies -- templates reference these paths (e.g. {{app.name}},
// {{proxies.#.host}}) and must keep resolving identically.
func TestGetAppsSubstitutionJSon_ShapePreserved(t *testing.T) {
	cluster, appWithRoutes, _ := newSubstitutionTestCluster(t)

	result, err := cluster.GetAppsSubstitutionJSon(appWithRoutes)
	if err != nil {
		t.Fatalf("GetAppsSubstitutionJSon failed: %v", err)
	}

	cases := []struct {
		path string
		want string
	}{
		{"name", "pfx-cluster"},
		{"servers.0.host", "10.0.0.1"},
		{"proxies.0.host", "10.0.0.5"},
		{"proxies.0.version", "1.0.0"},
		{"apps.0.name", "app1"},
		{"apps.0.host", "app1.internal"},
		{"apps.0.config.appDbUser", "dbuser"},
		{"apps.0.config.appDbSchema", "dbschema"},
		{"apps.0.config.deployment.routes.0.cname", "app1.example.com"},
		{"apps.0.config.deployment.primaryRoute.cname", "app1.example.com"},
		{"apps.1.name", "app2"},
		{"app.name", "app1"},
		{"app.config.deployment.routes.0.cname", "app1.example.com"},
	}
	for _, tc := range cases {
		got := gjson.Get(result, tc.path)
		if !got.Exists() {
			t.Fatalf("path %q: not found in %s", tc.path, result)
		}
		if got.String() != tc.want {
			t.Fatalf("path %q: got %q, want %q", tc.path, got.String(), tc.want)
		}
	}
}

// TestGetAppsSubstitutionJSon_NilRoutesStayNull guards the specific detail
// buildAppSubstitutionView has to get right by hand (sheriff did this
// automatically): a nil config.Routes must still render as JSON null, not
// an empty array, both in the "apps" list and the singular "app" key.
func TestGetAppsSubstitutionJSon_NilRoutesStayNull(t *testing.T) {
	cluster, _, appNoRoutes := newSubstitutionTestCluster(t)

	result, err := cluster.GetAppsSubstitutionJSon(appNoRoutes)
	if err != nil {
		t.Fatalf("GetAppsSubstitutionJSon failed: %v", err)
	}

	routesVal := gjson.Get(result, "apps.1.config.deployment.routes")
	if routesVal.Type != gjson.Null {
		t.Fatalf("expected nil Routes to render as JSON null in apps list, got %s (type %v)", routesVal.Raw, routesVal.Type)
	}

	singularRoutes := gjson.Get(result, "app.config.deployment.routes")
	if singularRoutes.Type != gjson.Null {
		t.Fatalf("expected nil Routes on singular app to render as JSON null, got %s (type %v)", singularRoutes.Raw, singularRoutes.Type)
	}
}

// TestGetAppsSubstitutionJSon_ConcurrentRefreshAndListRebuildNoRace exercises
// three concurrent-mutation classes GetAppsSubstitutionJSon must survive
// under -race:
//
//   - CheckPrimaryRoute() mutating Deployment.Routes/PrimaryRoute on the app
//     being marshaled -- fixed by cloning AppConfig under app.Lock().
//   - cluster.Conf.Apps holding the *same* *config.AppConfig pointer as
//     app.AppConfig (see GetAppConfig): a second live path to the exact
//     data just cloned, which has to be closed too, not just the App-list
//     path.
//   - cluster.Apps / cluster.Conf.Apps being reassigned wholesale, as
//     newAppList does under cluster.Lock(), while a marshal is in flight.
//
// Run with -race; a regression here reproduces immediately rather than
// needing a specific timing window.
func TestGetAppsSubstitutionJSon_ConcurrentRefreshAndListRebuildNoRace(t *testing.T) {
	cluster, appWithRoutes, appNoRoutes := newSubstitutionTestCluster(t)
	cluster.Conf.Apps = []*config.AppConfig{appWithRoutes.AppConfig, appNoRoutes.AppConfig}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Mutator 1: repeatedly runs CheckPrimaryRoute on the live app, exactly
	// as the refresh loop does every tick.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				appWithRoutes.CheckPrimaryRoute()
			}
		}
	}()

	// Mutator 2: repeatedly rebuilds cluster.Apps / cluster.Conf.Apps under
	// cluster.Lock(), mirroring newAppList's atomic swap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cluster.Lock()
				cluster.Apps = []*App{appWithRoutes, appNoRoutes}
				cluster.Conf.Apps = []*config.AppConfig{appWithRoutes.AppConfig, appNoRoutes.AppConfig}
				cluster.Unlock()
			}
		}
	}()

	// Reader: repeatedly builds the substitution JSON, as every app refresh does.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := cluster.GetAppsSubstitutionJSon(appWithRoutes); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("GetAppsSubstitutionJSon failed: %v", err)
		}
	}

	close(stop)
	wg.Wait()
}

// TestGetAppsSubstitutionJSon_NilAppsStayNull guards the regression this fix
// introduced and then fixed: cluster.Apps/cluster.Conf.Apps being nil must
// still produce "apps": null (and cluster.Conf's "apps": null), not an
// empty array. ParseAppTemplate distinguishes "no value" (leaves a
// placeholder like {{apps.#.host}} unresolved) from "empty array" (resolves
// to ""), so collapsing nil into [] silently changes template behavior --
// see TestResolveEnvVariableValue_NoSubstitutionData_KeepsRaw.
func TestGetAppsSubstitutionJSon_NilAppsStayNull(t *testing.T) {
	cluster := &Cluster{
		Name: "empty-cluster",
		Conf: &config.Config{Timeout: 5},
	}
	app := &App{Id: "solo", Name: "solo", Mutex: &sync.Mutex{}, ClusterGroup: cluster}
	cluster.Apps = []*App{app}

	result, err := cluster.GetAppsSubstitutionJSon(app)
	if err != nil {
		t.Fatalf("GetAppsSubstitutionJSon failed: %v", err)
	}

	if v := gjson.Get(result, "config.apps"); v.Type != gjson.Null {
		t.Fatalf(`expected nil cluster.Conf.Apps to render as "config.apps": null, got %s (type %v)`, v.Raw, v.Type)
	}
}

// TestGetAppsSubstitutionJSon_ConcurrentServerRefreshNoRace guards the
// proven Servers race: ServerMonitor.SetState (cluster/srv_set.go:76) is
// unlocked, exactly like App.SetState and Proxy.SetState were, and runs
// continuously as part of normal DB topology monitoring -- a goroutine
// independent of app-refresh workers. Before serverSubstitutionView
// existed, this test failed under -race (confirmed): sheriff's
// reflect.Value.Interface() bulk-copies the whole live ServerMonitor
// struct before its own tag filtering runs, so mutating State raced
// against GetAppsSubstitutionJSon reflecting live cluster.Servers, even
// though State itself isn't groups:"apps"-tagged.
func TestGetAppsSubstitutionJSon_ConcurrentServerRefreshNoRace(t *testing.T) {
	cluster, appWithRoutes, _ := newSubstitutionTestCluster(t)
	server := cluster.Servers[0]

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				server.SetState(stateSuspect)
			}
		}
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := cluster.GetAppsSubstitutionJSon(appWithRoutes); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("GetAppsSubstitutionJSon failed: %v", err)
		}
	}

	close(stop)
	wg.Wait()
}

// TestGetAppsSubstitutionJSon_ConcurrentProxyRefreshNoRace guards the
// proven Proxies race: refreshProxies (cluster/prx.go:457-502) runs as its
// own goroutine, concurrently with app-refresh workers building this same
// substitution JSON. Version is the one groups:"apps"-tagged Proxy field
// the refresh loop actively mutates (see e.g. HaproxyProxy/ProxySQLProxy
// Refresh()), so this mimics that write pattern -- SetVersion(), exactly as
// the real Refresh() methods now do -- against a concurrent
// GetAppsSubstitutionJSon reader.
func TestGetAppsSubstitutionJSon_ConcurrentProxyRefreshNoRace(t *testing.T) {
	cluster, appWithRoutes, _ := newSubstitutionTestCluster(t)
	proxy := cluster.Proxies[0].(*HaproxyProxy)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				proxy.SetVersion("1.2.3")
			}
		}
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := cluster.GetAppsSubstitutionJSon(appWithRoutes); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("GetAppsSubstitutionJSon failed: %v", err)
		}
	}

	close(stop)
	wg.Wait()
}
