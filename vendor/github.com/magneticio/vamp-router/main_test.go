// +build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/magneticio/vamp-router/haproxy"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file contains the initial integration tests for Vamp Router. Integration tests rely on some
// external components:
// - Docker
// - Haproxy

const (
	TEMPLATE_FILE = "configuration/templates/haproxy_config.template"
	CONFIG_FILE   = "haproxy_test.cfg"
	JSON_FILE     = "vamp_lb_test.json"
	PID_FILE      = "vamp_lb_test.pid"
	TEST_FILES    = "test/integration/"
	WORK_DIR      = "/tmp/vamp_router_integration_test/"
	DOCKER_HOST   = "192.168.59.103"
)

// TestHarnass is an object that holds the prerequisites for setting up a test scenario
// such as the config for Vamp Router and the containers that should be run.
type TestHarnass struct {
	Name       string         `json:"name"`
	Containers []*Container   `json:"containers"`
	Config     haproxy.Config `json:"config"`
	UseCookies bool           `json:"useCookies"`
	Cases      []*Case        `json:"cases"`
}

type Case struct {
	Url     string    `json:"url"`
	Verb    string    `json:"verb"`
	Headers []*Header `json:"headers"`
	Expect  string    `json:"expect"`
}

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Container struct {
	OutPort    int    `json:"outPort"`
	InPort     int    `json:"inPort"`
	Name       string `json:"name"`
	Image      string `json:"image"`
	Parameters string `json:"parameters"`
}

// Does the actual request and checks the result
func (th *TestHarnass) Assert() bool {

	// create a cookie jar, http client and some helper variables
	jar, _ := cookiejar.New(nil)
	var cookies []*http.Cookie
	result := true
	client := &http.Client{}

	// loop over all cases
	for i, _case := range th.Cases {

		req, err := http.NewRequest(_case.Verb, _case.Url, nil)

		for _, header := range _case.Headers {
			req.Header.Set(header.Key, header.Value)
		}

		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}

		defer resp.Body.Close()

		// if the test needs to persist cookies between request, do so
		if th.UseCookies {
			cookies = resp.Cookies()
			u, _ := url.Parse(_case.Url)
			jar.SetCookies(u, cookies)
			client.Jar = jar
		}

		body, _ := ioutil.ReadAll(resp.Body)
		body = bytes.TrimRight(body, "\n")

		if !(string(body) == _case.Expect) {
			fmt.Printf("=== Error in request %s \n", strconv.Itoa(i+1))
			fmt.Printf("--- Expected: %s \n", _case.Expect)
			fmt.Printf("--- Result  : %s \n", body)
			result = false
			break
		} else {
			fmt.Printf("--- Correct result  : %s \n", body)
		}
	}
	return result
}

// TestMain sets up Vamp Router for testing and kicks of the tests.
// After the tests are done, a destroyTestHarnass routine is run.
func TestMain(m *testing.M) {

	setup()

	fmt.Println("--- Starting tests...")

	m.Run()

	defer KillHaproxy()
}

//Tests follow a pattern of loading the harnass and then testing assumptions.
func TestAll(t *testing.T) {

	files, _ := ioutil.ReadDir(TEST_FILES)

	for _, file := range files {

		if strings.HasSuffix(file.Name(), ".json") {
			th := loadTestHarnass(file.Name(), t)

			if !(th.Assert()) {
				t.Error("Failed test: ", th.Name)
			}

			destroyTestHarnass(th)
		}
	}
}

func setup() {

	fmt.Println("--- Running setup...")

	go setupApi()

}

func setupApi() {

	RemoveWorkingDir()

	KillHaproxy()

	main()

}

func runDocker(c *Container, wg *sync.WaitGroup) {

	defer wg.Done()

	name := "--name=\"" + c.Name + "\""
	portMap := strconv.Itoa(c.OutPort) + ":" + strconv.Itoa(c.InPort)

	fmt.Println("--- docker run -d " + name + " -p " + portMap + " " + c.Image + " " + c.Parameters)

	Docker := exec.Command("docker", "run", "-d", name, "-p", portMap, c.Image, c.Parameters)
	_ = Docker.Run()

	// check if Docker container has started
	for {

		time.Sleep(1000 * time.Millisecond)

		if resp, _ := http.Get("http://" + DOCKER_HOST + ":" + strconv.Itoa(c.OutPort) + "/ping"); resp.StatusCode == 200 {
			fmt.Printf("--- Container %s is running\n", c.Name)
			break
		}
	}

}

func stopDocker(c *Container, wg *sync.WaitGroup) {

	defer wg.Done()

	DockerStop := exec.Command("docker", "stop", c.Name)
	_ = DockerStop.Run()

	for {
		DockerCheck := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", c.Name)
		out, err := DockerCheck.Output()

		if err != nil {
			panic(err.Error())
		}

		if string(bytes.TrimRight(out, "\n")) == "false" {
			DockerRm := exec.Command("docker", "rm", c.Name)
			_ = DockerRm.Run()
			break
		}
		time.Sleep(1000 * time.Millisecond)

	}

}

func loadTestHarnass(file string, t *testing.T) *TestHarnass {

	var th *TestHarnass
	file_loc := TEST_FILES + file

	if f, err := ioutil.ReadFile(file_loc); err != nil {
		t.Errorf("Failed to load test harnass: %s", err.Error())
	} else {
		if err := json.Unmarshal(f, &th); err != nil {
			t.Errorf("Failed to load test harnass: %s", err.Error())
		}
	}

	fmt.Println()
	fmt.Println("--- Loading test harnass: ", th.Name)

	fmt.Println("--- Setting up containers...")

	var wg sync.WaitGroup

	for _, c := range th.Containers {

		wg.Add(1)

		go runDocker(c, &wg)

	}

	wg.Wait()

	fmt.Println("--- Setting up Vamp Router...")

	config, err := json.Marshal(th.Config)
	if err != nil {
		t.Errorf("Failed to load test harnass: %s", err.Error())
	}

	if resp, err := http.Post("http://localhost:10001/v1/config", "application/json", bytes.NewBuffer(config)); err != nil || resp.StatusCode != 201 {
		t.Errorf("Failed to load test harnass: %s", err.Error())
	}

	time.Sleep(4000 * time.Millisecond)

	return th
}

func destroyTestHarnass(th *TestHarnass) {

	fmt.Println("--- Destroying containers...")
	fmt.Println()

	var wg sync.WaitGroup

	for _, c := range th.Containers {

		wg.Add(1)

		go stopDocker(c, &wg)

	}

	wg.Wait()

}

func KillHaproxy() {

	fmt.Println("--- Destroying haproxy...")

	KillHaproxy := exec.Command("killall", "haproxy")
	_ = KillHaproxy.Run()

}

func RemoveWorkingDir() {

	fmt.Println("--- Destroying working dir...")

	os.RemoveAll(workDir.Dir())
}
