package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func sandboxTestApp(runtime string, cloud18 bool) (*Cluster, *App) {
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvType: "docker", ProvAppDockerRuntime: runtime, Cloud18: cloud18, ProvDBDockerRunArgsLimit: true}}
	a := &App{Name: "web", ClusterGroup: c, AppConfig: &config.AppConfig{
		ProvAppDockerImg: "nginx:1.27", ProvAppMem: "1024", ProvAppCpuCores: "2", ProvAppHATopology: "flex", ProvAppAgents: "n1,n2",
		Deployment: config.NewDeploymentConfig(),
	}}
	return c, a
}

// The runtime the setting resolves to: auto is the sandbox on a Cloud18 instance only,
// none and empty are off, a name is taken as is, anything else is refused (never a silent
// fallback to the default runtime).
func TestGetAppDockerRuntime(t *testing.T) {
	for _, tc := range []struct {
		value   string
		cloud18 bool
		want    string
		wantErr bool
	}{
		{"auto", true, "runsc-hostnet", false},
		{"auto", false, "", false},
		{"", true, "", false},
		{"none", true, "", false},
		{"runsc-hostnet", false, "runsc-hostnet", false},
		{"kata", true, "kata", false},
		{"runc --privileged", true, "", true},
		{"--privileged", true, "", true},
		{"a'b", true, "", true},
	} {
		conf := &config.Config{ProvAppDockerRuntime: tc.value, Cloud18: tc.cloud18}
		got, err := conf.GetAppDockerRuntime()
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("GetAppDockerRuntime(%q, cloud18=%v) = %q, %v; want %q, err=%v", tc.value, tc.cloud18, got, err, tc.want, tc.wantErr)
		}
		if verr := config.ValidateAppDockerRuntime(tc.value); (verr != nil) != tc.wantErr {
			t.Errorf("ValidateAppDockerRuntime(%q) = %v, want err=%v", tc.value, verr, tc.wantErr)
		}
	}
}

// The runtime goes at the end of run_args of the containers of the client's image only:
// the app, its reload task, its jobs sidecar; never the pause, the sensor, the init or
// mount sidecars.
func TestAppDockerRuntimeOnClientImageSectionsOnly(t *testing.T) {
	c, a := sandboxTestApp("auto", true)
	a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "APP_RELOAD_COMMAND", Type: config.VariableTypeEnv, Value: "kill -HUP 1"}}
	s, err := c.OpenSVCGetAppTemplateSectionMap(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := s["container#app"]["run_args"]; !strings.HasSuffix(got, " --runtime=runsc-hostnet") || !strings.Contains(got, "--cpus=2.0") {
		t.Fatalf("container#app run_args: %q", got)
	}
	// the reload cannot join the sandbox's PID namespace from another container: it runs IN
	// container#app (host task, `container` keyword), never on the node nor in a runc container
	if r := s["task#reload"]; r["type"] != "host" || r["container"] != "container#app" || r["image"] != "" || r["pidns"] != "" ||
		r["run_args"] != "" || r["command"] != `/bin/sh -c 'eval "$APP_RELOAD_COMMAND"'` || r["configs_environment"] != "web/*" {
		t.Fatalf("task#reload under the sandbox: %v", r)
	}
	if got := s["container#01"]["run_args"]; strings.Contains(got, "--runtime") {
		t.Fatalf("the pause container keeps the node's runtime: %q", got)
	}

	// the jobs sidecar (client image) gets it, its init container (ours) does not
	jc, ja := jobsTestApp("postgres", &config.Volume{Name: "pg1-drbd"})
	jc.Conf.ProvAppDockerRuntime = "runsc-hostnet"
	js := map[string]map[string]string{"volume#1": {"name": "pg1-drbd"}, "container#sensor": {"type": "docker", "image": "signal18/sensor"}}
	n := 1
	jc.openSVCAddAppJobsSections(js, ja, &n)
	if err := jc.applyAppDockerRuntime(js); err != nil {
		t.Fatal(err)
	}
	if js["container#jobs"]["run_args"] != "--runtime=runsc-hostnet" {
		t.Fatalf("container#jobs: %v", js["container#jobs"])
	}
	for _, name := range []string{"container#02initjobs", "container#sensor"} {
		if strings.Contains(js[name]["run_args"], "--runtime") {
			t.Fatalf("%s keeps the node's runtime: %v", name, js[name])
		}
	}
}

// Off (auto on a non-Cloud18 instance, none, empty): no --runtime anywhere.
func TestAppDockerRuntimeOff(t *testing.T) {
	for _, tc := range []struct {
		value   string
		cloud18 bool
	}{{"auto", false}, {"none", true}, {"", true}} {
		c, a := sandboxTestApp(tc.value, tc.cloud18)
		a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "APP_RELOAD_COMMAND", Type: config.VariableTypeEnv, Value: "kill -HUP 1"}}
		s, err := c.OpenSVCGetAppTemplateSectionMap(a)
		if err != nil {
			t.Fatal(err)
		}
		// off: the reload stays a container of the app image in the app's PID namespace
		if r := s["task#reload"]; r["type"] != "docker" || r["pidns"] != "container#app" {
			t.Fatalf("%q cloud18=%v: task#reload off the sandbox: %v", tc.value, tc.cloud18, r)
		}
		for name, sec := range s {
			if strings.Contains(sec["run_args"], "--runtime") {
				t.Fatalf("%q cloud18=%v: %s has a runtime: %q", tc.value, tc.cloud18, name, sec["run_args"])
			}
		}
	}
	// an invalid value in a configuration file stops the rendering
	c, a := sandboxTestApp("runc --privileged", true)
	if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err == nil {
		t.Fatal("an invalid runtime renders nothing")
	}
}

// The client's values cannot reach the run options, the mounts or a new keyword: each is
// refused with an error, the sandbox stays the last run option.
func TestAppClientValuesCannotOverrideSandbox(t *testing.T) {
	refused := map[string]func(a *App){
		"cores with options":     func(a *App) { a.AppConfig.ProvAppCpuCores = "1 --privileged --runtime=runc" },
		"image read as option":   func(a *App) { a.AppConfig.ProvAppDockerImg = "--runtime=runc" },
		"image with space":       func(a *App) { a.AppConfig.ProvAppDockerImg = "nginx --privileged" },
		"command line break":     func(a *App) { a.AppConfig.ProvAppDockerCmd = "sleep 1\nprivileged = true" },
		"mount target host path": func(a *App) { addPath(a, "/data /:/host:rw") },
		"mount target options":   func(a *App) { addPath(a, "/data:rshared") },
		"mount source traversal": func(a *App) { addPathSource(a, "/data", "../../etc") },
		"volume name host path":  func(a *App) { a.AppConfig.Deployment.Storages.Volumes = config.Volumes{{Name: "/"}} },
		"volume dir traversal": func(a *App) {
			a.AppConfig.Deployment.Storages.Volumes = config.Volumes{{Name: "v1", VolumeDir: "data ../../etc"}}
		},
		"volume dirperm": func(a *App) {
			a.AppConfig.Deployment.Storages.Volumes = config.Volumes{{Name: "v1", VolumeDir: "data", DirPerm: "777 x"}}
		},
		"git clone volume host path": func(a *App) {
			a.AppConfig.Deployment.Storages.GitClones = config.GitClones{{Name: "src", VolumeName: "/var/run"}}
		},
		"git clone dir shell": func(a *App) {
			a.AppConfig.Deployment.Storages.GitClones = config.GitClones{{Name: "src", VolumeName: "v1", VolumeDir: "x';sh;'"}}
		},
		"s3 uid option": func(a *App) {
			a.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{{Name: "s3", VolumeName: "v1", Uid: "33 -o x"}}
		},
		"s3 loader variable": func(a *App) {
			a.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{{Name: "s3", VolumeName: "v1"}}
			a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "LD_PRELOAD", Type: config.VariableTypeEnv, Value: "/mnt/x.so"}}
		},
	}
	for name, mutate := range refused {
		c, a := sandboxTestApp("runsc-hostnet", true)
		mutate(a)
		// refused by the validation, before the orchestrator is asked anything (the pool
		// list of a volume): the error names the value
		if err := c.validateAppDeploymentForService(a); err == nil && name != "command line break" {
			t.Errorf("%s: accepted by the validation", name)
		}
		if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err == nil {
			t.Errorf("%s: rendered, want refused", name)
		}
	}

	// the client keeps any image and command; the command never reaches run_args
	c, a := sandboxTestApp("runsc-hostnet", true)
	a.AppConfig.ProvAppDockerImg = "registry.example.com/team/app@sha256:0123abcd"
	a.AppConfig.ProvAppDockerCmd = "--runtime=runc --privileged sh -c 'echo ok'"
	s, err := c.OpenSVCGetAppTemplateSectionMap(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(s["container#app"]["run_args"], "--runtime=runsc-hostnet") || strings.Contains(s["container#app"]["run_args"], "runc ") {
		t.Fatalf("run_args: %q", s["container#app"]["run_args"])
	}
	if s["container#app"]["run_command"] != a.AppConfig.ProvAppDockerCmd {
		t.Fatalf("the command is the container's arguments: %q", s["container#app"]["run_command"])
	}

	// the app settings refuse the same values early
	for key, value := range map[string]string{
		"prov-app-docker-img": "--privileged",
		"prov-app-cpu-cores":  "2 --privileged",
		"prov-app-docker-cmd": "a\nb",
	} {
		if err := a.SetSetting(key, value); err == nil {
			t.Errorf("SetSetting(%s, %q) accepted", key, value)
		}
	}
}

func addPath(a *App, dockerPath string) { addPathSource(a, dockerPath, ".") }

func addPathSource(a *App, dockerPath, srcPath string) {
	a.AppConfig.Deployment.Paths = append(a.AppConfig.Deployment.Paths, &config.PathMapping{
		Name: "p", DockerPath: dockerPath, SourceType: config.SourceVolume, SourceName: "v1", SourcePath: srcPath, VolumeName: "v1",
	})
}

// SafeDockerMapping keeps one mount per path: a space, a ':' or a '..' is refused.
func TestSafeDockerMappingRefusesInjection(t *testing.T) {
	ok := config.PathMapping{DockerPath: "/var/www/html", SourcePath: "data"}
	if got, err := ok.SafeDockerMapping("v1"); err != nil || got != "v1/data:/var/www/html" {
		t.Fatalf("SafeDockerMapping = %q, %v", got, err)
	}
	for _, p := range []config.PathMapping{
		{DockerPath: "/data /:/host", SourcePath: "."},
		{DockerPath: "/data:rw,rshared", SourcePath: "."},
		{DockerPath: "relative", SourcePath: "."},
		{DockerPath: "/data", SourcePath: "a b"},
		{DockerPath: "/data", SourcePath: "../x"},
		{DockerPath: "/a/../../x", SourcePath: "."},
	} {
		if got, err := p.SafeDockerMapping("v1"); err == nil {
			t.Errorf("SafeDockerMapping(%q, %q) = %q, want refused", p.DockerPath, p.SourcePath, got)
		}
	}
}
