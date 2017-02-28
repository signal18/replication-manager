// maxscale.go

package maxscale

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type MaxScale struct {
	Host string
	Port string
	User string
	Pass string
	Conn net.Conn
}

type Server struct {
	Server      string
	Address     string
	Port        string
	Connections string
	Status      string
}

type ServerMaxinfo struct {
	Server      string
	Address     string
	Port        int
	Connections int
	Status      string
}

type MonitorMaxinfo struct {
	Monitor string
	Status  string
}
type Monitor struct {
	Monitor string
	Status  string
}

var ServerList = make([]Server, 0)
var MonitorList = make([]Monitor, 0)

var ServerMaxinfos = make([]ServerMaxinfo, 0)
var MonitorMaxinfos = make([]MonitorMaxinfo, 0)

const (
	maxDefaultPort    = "6603"
	maxDefaultUser    = "admin"
	maxDefaultPass    = "mariadb"
	maxDefaultTimeout = (1 * time.Second)
	// Error types
	ErrorNegotiation = "Incorrect maxscale protocol negotiation"
	ErrorReader      = "Error reading from buffer"
)

func (m *MaxScale) Connect() error {
	var err error
	address := fmt.Sprintf("%s:%s", m.Host, m.Port)
	m.Conn, err = net.DialTimeout("tcp", address, maxDefaultTimeout)
	if err != nil {
		return errors.New(fmt.Sprintf("Connection failed to address %s", address))
	}
	reader := bufio.NewReader(m.Conn)
	buf := make([]byte, 80)
	res, err := reader.Read(buf)
	if err != nil {
		return errors.New(ErrorReader)
	}
	if res != 4 {
		return errors.New(ErrorNegotiation)
	}
	writer := bufio.NewWriter(m.Conn)
	fmt.Fprint(writer, m.User)
	writer.Flush()
	res, err = reader.Read(buf)
	if err != nil {
		return errors.New(ErrorReader)
	}
	if res != 8 {
		return errors.New(ErrorNegotiation)
	}
	fmt.Fprint(writer, m.Pass)
	writer.Flush()
	res, err = reader.Read(buf)
	if err != nil {
		return errors.New(ErrorReader)
	}
	if string(buf[0:6]) == "FAILED" {
		return errors.New("Authentication failed")
	}
	return nil
}

func (m *MaxScale) Close() {
	if m.Conn != nil {
		m.Conn.Close()
	}
}
func (m *MaxScale) GetMaxInfoServers(url string) ([]ServerMaxinfo, error) {
	client := &http.Client{}

	// Send the request via a client
	// Do sends an HTTP request and
	// returns an HTTP response
	// Build the request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal("NewRequest: ", err)
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal("Do: ", err)
		return nil, err
	}

	// Callers should close resp.Body
	// when done reading from it
	// Defer the closing of the body
	defer resp.Body.Close()
	monjson, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("Do: ", err)
		return nil, err
	}

	// Use json.Decode for reading streams of JSON data
	if err := json.Unmarshal(monjson, &ServerMaxinfos); err != nil {
		log.Println(err)
	}
	return ServerMaxinfos, nil
}

func (m *MaxScale) GetMaxInfoMonitors(url string) ([]MonitorMaxinfo, error) {
	client := &http.Client{}

	// Send the request via a client
	// Do sends an HTTP request and
	// returns an HTTP response
	// Build the request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal("NewRequest: ", err)
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal("Do: ", err)
		return nil, err
	}

	// Callers should close resp.Body
	// when done reading from it
	// Defer the closing of the body
	defer resp.Body.Close()
	monjson, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("Do: ", err)
		return nil, err
	}

	// Use json.Decode for reading streams of JSON data
	if err := json.Unmarshal(monjson, &MonitorMaxinfos); err != nil {
		log.Println(err)
	}
	return MonitorMaxinfos, nil
}

func (m *MaxScale) ShowServers() ([]byte, error) {
	m.Command("show serversjson")
	reader := bufio.NewReader(m.Conn)
	var response []byte
	buf := make([]byte, 80)
	for {
		res, err := reader.Read(buf)
		if err != nil {
		}
		str := string(buf[0:res])
		if res < 80 && strings.HasSuffix(str, "OK") {
			response = append(response, buf[0:res-2]...)
			break
		}
		response = append(response, buf[0:res]...)
	}
	return response, nil
}

func (m *MaxScale) ListServers() ([]Server, error) {
	m.Command("list servers")
	reader := bufio.NewReader(m.Conn)
	var response []byte
	buf := make([]byte, 1024)
	for {
		res, err := reader.Read(buf)

		if err != nil {
			return ServerList, nil
		}
		str := string(buf[0:res])
		//	log.Println(str)
		if strings.HasSuffix(str, "OK") {

			response = append(response, buf[0:res-2]...)
			break
		}
		response = append(response, buf[0:res]...)
	}

	list := strings.Split(string(response), "\n")

	for _, line := range list {
		//log.Println(line)
		re := regexp.MustCompile(`^([[:graph:]]+)[[:space:]]*\|[[:space:]]*([[:graph:]]+)[[:space:]]*\|[[:space:]]*([0-9]+)[[:space:]]*\|[[:space:]]*([0-9]+)[[:space:]]*\|[[:space:]]*([[:ascii:]]+)*`)

		match := re.FindStringSubmatch(line)

		if len(match) > 0 {
			if match[0] != "" && match[1] != "Server" {

				item := Server{Server: match[1], Address: match[2], Port: match[3], Connections: match[4], Status: match[5]}
				ServerList = append(ServerList, item)
			}
		}
	}
	return ServerList, nil

}

func (m *MaxScale) ListMonitors() ([]Monitor, error) {
	err := m.Command("list monitors")
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(m.Conn)
	var response []byte
	buf := make([]byte, 80)
	for {
		res, err := reader.Read(buf)
		if err != nil {
		}
		str := string(buf[0:res])
		if res < 80 && strings.HasSuffix(str, "OK") {
			response = append(response, buf[0:res-2]...)
			break
		}
		response = append(response, buf[0:res]...)
	}
	list := strings.Split(string(response), "\n")

	for _, line := range list {
		re := regexp.MustCompile(`^([[:ascii:]]+)*\|[[:space:]]*([[:ascii:]]+)*`)
		match := re.FindStringSubmatch(line)
		if len(match) > 0 {
			if match[0] != "" && match[1] != "Monitor" {
				item := Monitor{Monitor: strings.TrimRight(match[1], " "), Status: strings.TrimRight(match[2], " ")}
				MonitorList = append(MonitorList, item)
			}
		}
	}
	return MonitorList, nil
}

func (m *MaxScale) GetMonitor() string {
	for _, s := range MonitorList {
		if s.Status == "Running" {
			return s.Monitor
		}
	}
	return ""
}

func (m *MaxScale) GetMaxInfoMonitor() string {
	for _, s := range MonitorMaxinfos {
		if s.Status == "Running" {
			return s.Monitor
		}
	}
	return ""
}

func (m *MaxScale) GetServer(ip string, port string) (string, string, string) {
	for _, s := range ServerList {
		if s.Address == ip && s.Port == port {
			return s.Server, s.Connections, s.Status
		}
	}
	return "", "", ""
}

func (m *MaxScale) GetMaxInfoServer(ip string, port int) (string, string, int) {
	for _, s := range ServerMaxinfos {
		//	log.Printf("%s,%s", s.Address, ip)
		if s.Address == ip && s.Port == port {
			return s.Server, s.Status, s.Connections
		}
	}
	return "", "", 0
}

func (m *MaxScale) Command(cmd string) error {
	writer := bufio.NewWriter(m.Conn)
	if _, err := fmt.Fprint(writer, cmd); err != nil {
		return err
	}
	err := writer.Flush()
	return err
}

func (m *MaxScale) ShutdownMonitor(monitor string) error {
	writer := bufio.NewWriter(m.Conn)
	if _, err := fmt.Fprintf(writer, "shutdown monitor %c%s%c\n", '"', monitor, '"'); err != nil {
		return err
	}
	err := writer.Flush()
	return err
}
