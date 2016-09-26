// maxscale.go

package maxscale

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

type MaxScale struct {
	host string
	port string
	user string
	pass string
	conn net.Conn
}

const (
	maxDefaultPort    = "6603"
	maxDefaultUser    = "admin"
	maxDefaultPass    = "mariadb"
	maxDefaultTimeout = (10 * time.Second)
	// Error types
	ErrorNegotiation = "Incorrect maxscale protocol negotiation"
	ErrorReader      = "Error reading from buffer"
)

func (m *MaxScale) connect() error {
	var err error
	address := fmt.Sprintf("%s:%s", m.host, m.port)
	m.conn, err = net.DialTimeout("tcp", address, maxDefaultTimeout)
	if err != nil {
		return errors.New(fmt.Sprintf("Connection failed to address %s", address))
	}
	reader := bufio.NewReader(m.conn)
	buf := make([]byte, 80)
	res, err := reader.Read(buf)
	if err != nil {
		return errors.New(ErrorReader)
	}
	if res != 4 {
		return errors.New(ErrorNegotiation)
	}
	writer := bufio.NewWriter(m.conn)
	fmt.Fprint(writer, m.user)
	writer.Flush()
	res, err = reader.Read(buf)
	if err != nil {
		return errors.New(ErrorReader)
	}
	if res != 8 {
		return errors.New(ErrorNegotiation)
	}
	fmt.Fprint(writer, m.pass)
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

func (m *MaxScale) showServers() ([]byte, error) {
	writer := bufio.NewWriter(m.conn)
	fmt.Fprint(writer, "show serversjson")
	writer.Flush()
	reader := bufio.NewReader(m.conn)
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
