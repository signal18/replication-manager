package cluster

import (
	"os"
	"os/exec"
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
		r["run_args"] != "" || r["command"] != appReloadSandboxCommand("runsc-hostnet") || r["configs_environment"] != "web/*" || r["shared"] != "" {
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
		"s3 endpoint words": func(a *App) {
			a.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{{Name: "s3", VolumeName: "v1", Endpoint: "h:9000 -o allow_root"}}
		},
		"s3 bucket option": func(a *App) {
			a.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{{Name: "s3", VolumeName: "v1", Bucket: "--debug_fuse"}}
		},
		"s3 name section": func(a *App) {
			a.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{{Name: "s3]\n[x", VolumeName: "v1"}}
		},
		"memory not a size": func(a *App) { a.AppConfig.ProvAppMem = "1g --privileged" },
		"cores zero":        func(a *App) { a.AppConfig.ProvAppCpuCores = "0" },
		"cores exponent":    func(a *App) { a.AppConfig.ProvAppCpuCores = "1e3" },
		"cores negative":    func(a *App) { a.AppConfig.ProvAppCpuCores = "-1" },
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

// Decimal cores are taken as docker --cpus takes them; a whole number keeps its N.0 form.
func TestAppCoresDecimal(t *testing.T) {
	for raw, want := range map[string]string{"2": "--cpus=2.0", "0.5": "--cpus=0.5", "1.5": "--cpus=1.5", " 4 ": "--cpus=4.0"} {
		c, a := sandboxTestApp("none", false)
		a.AppConfig.ProvAppCpuCores = raw
		s, err := c.OpenSVCGetAppTemplateSectionMap(a)
		if err != nil {
			t.Fatalf("cores %q: %v", raw, err)
		}
		if got := s["container#app"]["run_args"]; !strings.HasSuffix(got, want) || !strings.Contains(got, "--memory=1024m --memory-swap=1024m ") {
			t.Errorf("cores %q: run_args %q, want suffix %q", raw, got, want)
		}
		if err := a.SetSetting("prov-app-cpu-cores", raw); err != nil {
			t.Errorf("SetSetting(prov-app-cpu-cores, %q): %v", raw, err)
		}
	}
	// the container section itself fails on a bad value: never rendered uncapped
	c, a := sandboxTestApp("none", false)
	a.AppConfig.ProvAppCpuCores = "two"
	if _, err := c.OpenSVCGetAppContainerSection(a); err == nil {
		t.Fatal("container#app rendered with cores \"two\"")
	}
}

func gitCloneTestApp(gc *config.GitClone) (*Cluster, *App) {
	c, a := sandboxTestApp("runsc-hostnet", true)
	a.AppConfig.Deployment.Storages.Volumes = config.Volumes{{Name: "v1", VolumeDir: "data"}}
	if gc.VolumeName == "" {
		gc.VolumeName = "v1"
	}
	a.AppConfig.Deployment.Storages.GitClones = config.GitClones{gc}
	return c, a
}

// A git clone name, branch or repository that is not one is refused before anything is
// rendered: shell syntax, a quote, a line break, a git option, another scheme.
func TestGitCloneValuesRefused(t *testing.T) {
	for name, gc := range map[string]*config.GitClone{
		"name shell":         {Name: "x;id;", GitRepo: "github.com/a/b.git"},
		"name expansion":     {Name: "x$(id)", GitRepo: "github.com/a/b.git"},
		"name space":         {Name: "x y", GitRepo: "github.com/a/b.git"},
		"branch subshell":    {Name: "src", GitBranch: "$(id)", GitRepo: "github.com/a/b.git"},
		"branch backtick":    {Name: "src", GitBranch: "`id`", GitRepo: "github.com/a/b.git"},
		"branch quote":       {Name: "src", GitBranch: `x"; id; "`, GitRepo: "github.com/a/b.git"},
		"branch semicolon":   {Name: "src", GitBranch: "main;id", GitRepo: "github.com/a/b.git"},
		"branch newline":     {Name: "src", GitBranch: "main\nid", GitRepo: "github.com/a/b.git"},
		"branch option":      {Name: "src", GitBranch: "--upload-pack=touch /tmp/x", GitRepo: "github.com/a/b.git"},
		"branch dotdot":      {Name: "src", GitBranch: "a..b", GitRepo: "github.com/a/b.git"},
		"branch lock":        {Name: "src", GitBranch: "main.lock", GitRepo: "github.com/a/b.git"},
		"repo option":        {Name: "src", GitRepo: "--upload-pack=touch /tmp/x"},
		"repo subshell":      {Name: "src", GitRepo: "github.com/$(id)"},
		"repo backtick":      {Name: "src", GitRepo: "github.com/`id`"},
		"repo quote":         {Name: "src", GitRepo: `github.com/a" "b`},
		"repo space":         {Name: "src", GitRepo: "github.com/a b"},
		"repo newline":       {Name: "src", GitRepo: "github.com/a\nb"},
		"repo ext transport": {Name: "src", GitRepo: "ext::sh -c id"},
		"repo file":          {Name: "src", GitRepo: "file:///etc"},
		"repo ssh":           {Name: "src", GitRepo: "ssh://git@github.com/a/b.git"},
		"repo scp form":      {Name: "src", GitRepo: "git@github.com:a/b.git"},
		"repo empty":         {Name: "src"},
	} {
		c, a := gitCloneTestApp(gc)
		if err := c.validateAppDeploymentForService(a); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
	for _, gc := range []*config.GitClone{
		{Name: "cloud18-git-sample-data", GitBranch: "main", GitRepo: "github.com/signal18/cloud18-template-code.git"},
		{Name: "src", GitBranch: "release/v1.2+build", GitRepo: "https://gitlab.example.com:8443/team/app.git"},
		{Name: "src.v2", GitRepo: "http://git.example.com/~team/app"},
	} {
		c, a := gitCloneTestApp(gc)
		if err := c.validateAppDeploymentForService(a); err != nil {
			t.Errorf("%+v: %v", gc, err)
		}
	}
}

// The clone command is a fixed script: run by a real shell (tokenized as OpenSVC's shlex
// does), with hostile values in the environment it reads, git receives each value as one
// literal argument and nothing else runs.
func TestGitCloneCommandNeverEvaluatesValues(t *testing.T) {
	gc := &config.GitClone{Name: "src-code", GitRepo: "github.com/a/b.git", VolumeDir: "www"}
	c, a := gitCloneTestApp(gc)
	if err := c.validateAppDeploymentForService(a); err != nil {
		t.Fatal(err)
	}
	// the section itself (the whole service would ask the orchestrator for the volume's pool)
	cmd := c.OpenSVCGetAppGitInitContainerSection(a, gc)["command"]
	if cmd == "" || strings.Contains(cmd, "github.com") {
		t.Fatalf("clone command %q: no client value in its text", cmd)
	}
	dir := t.TempDir()
	marker := dir + "/pwned"
	if err := os.WriteFile(dir+"/git", []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GIT_ARGS\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// the directory argument is /bootstrap/www in the container: rewritten to the temp dir
	cmd = strings.Replace(cmd, "/bootstrap/www", dir+"/www", 1)
	branch := `x"; touch ` + marker + `; "$(touch ` + marker + ")`" + "touch " + marker + "`"
	repo := "github.com/a$(touch " + marker + ")\";touch " + marker + ";\"`touch " + marker + "`"
	sh := exec.Command("/bin/sh", "-c", "/bin/sh "+cmd)
	sh.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "GIT_ARGS=" + dir + "/args", "GIT_SRC_CODE_BRANCH=" + branch, "GIT_SRC_CODE_REPO=" + repo}
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a value was evaluated by the shell")
	}
	args, err := os.ReadFile(dir + "/args")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"clone", "-b", branch, "--", "https://" + repo, dir + "/www"}, "\n") + "\n"
	if string(args) != want {
		t.Fatalf("git args:\n%s\nwant:\n%s", args, want)
	}
}

// The privileged S3 mount sidecar gets the keys of its own mount, under the names its
// entrypoint reads, never the app's environment.
func TestS3MountSidecarEnvironment(t *testing.T) {
	c, a := sandboxTestApp("runsc-hostnet", true)
	a.AppConfig.Deployment.Storages.Volumes = config.Volumes{{Name: "v1", VolumeDir: "data"}}
	a.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{{Name: "s3-pics", VolumeName: "v1", Endpoint: "minio.example.com:9000", Bucket: "pics"}}
	a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "LD_PRELOAD", Type: config.VariableTypeEnv, Value: "/mnt/x.so"}}
	if err := c.validateAppDeploymentForService(a); err != nil {
		t.Fatal(err)
	}
	side := c.OpenSVCGetAppS3MountContainerSection(a, a.AppConfig.Deployment.Storages.S3Mounts[0])
	if side == nil || side["privileged"] != "true" {
		t.Fatalf("S3 sidecar: %v", side)
	}
	if got, want := side["configs_environment"], "BUCKET=web/S3_PICS_BUCKET REGION=web/S3_PICS_REGION AWS_ACCESS_KEY=web/S3_PICS_AWS_ACCESS_KEY MOUNT_DIR=web/S3_PICS_MOUNT_DIR ENDPOINT=web/S3_PICS_ENDPOINT"; got != want {
		t.Fatalf("configs_environment %q, want %q", got, want)
	}
	if got := side["secrets_environment"]; got != "AWS_SECRET_KEY=web/S3_PICS_AWS_SECRET_KEY" {
		t.Fatalf("secrets_environment %q", got)
	}
}

// A refused definition is a state of the app (APPERR012) until one renders.
func TestServiceDefinitionErrorState(t *testing.T) {
	c, a := sandboxTestApp("runsc-hostnet", true)
	a.AppConfig.ProvAppDockerImg = "--privileged"
	if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err == nil {
		t.Fatal("rendered")
	}
	if msg := a.getServiceDefinitionError(); !strings.Contains(msg, "prov-app-docker-img") {
		t.Fatalf("ServiceDefinitionError %q", msg)
	}
	a.AppConfig.ProvAppDockerImg = "nginx:1.27"
	if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err != nil {
		t.Fatal(err)
	}
	if msg := a.getServiceDefinitionError(); msg != "" {
		t.Fatalf("cleared after a rendering, got %q", msg)
	}
}

// The reload under a sandbox: shared on a failover app; refused by the command itself
// anywhere but inside the sandbox (here, the test host: not gVisor, no container files),
// so an agent that ignored the `container` keyword runs nothing on the node.
func TestAppReloadSandboxTask(t *testing.T) {
	c, a := sandboxTestApp("runsc-hostnet", true)
	a.AppConfig.ProvAppHATopology = "failover"
	a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "APP_RELOAD_COMMAND", Type: config.VariableTypeEnv, Value: "kill -HUP 1"}}
	s, err := c.OpenSVCGetAppTemplateSectionMap(a)
	if err != nil {
		t.Fatal(err)
	}
	if r := s["task#reload"]; r["type"] != "host" || r["shared"] != "true" {
		t.Fatalf("failover reload task: %v", r)
	}
	for _, cmd := range []string{appReloadSandboxCommand("runsc-hostnet"), appReloadSandboxCommand("kata")} {
		if strings.Contains(cmd, " ;") || strings.Contains(cmd, " #") || strings.Contains(cmd, "{") {
			t.Fatalf("%q: an INI comment marker or an OpenSVC reference", cmd)
		}
		marker := t.TempDir() + "/ran"
		// OpenSVC runs a command with a ';' through /bin/sh -c: the same here
		sh := exec.Command("/bin/sh", "-c", cmd)
		sh.Env = []string{"PATH=/usr/bin:/bin", "APP_RELOAD_COMMAND=touch " + marker}
		out, err := sh.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "reload refused") {
			t.Fatalf("%q outside the sandbox: err=%v out=%s", cmd, err, out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%q ran the reload outside the sandbox", cmd)
		}
	}
	// an invalid runtime with a reload command renders nothing
	c, a = sandboxTestApp("runc --privileged", true)
	a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "APP_RELOAD_COMMAND", Type: config.VariableTypeEnv, Value: "kill -HUP 1"}}
	if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err == nil {
		t.Fatal("an invalid runtime with a reload task rendered")
	}
}
