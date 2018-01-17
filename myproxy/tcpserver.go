package myproxy

import (
	"database/sql"
	"log"
	"net"

	siddon "github.com/siddontang/go-mysql/server"
	"github.com/signal18/replication-manager/config"
)

type Server struct {
	cfg      *config.Config
	db       *sql.DB
	addr     string
	user     string
	password string

	running bool

	listener net.Listener
}

// StartProxyServer start tcp proxy server for Mysql.
func NewProxyServer(host string, user string, password string, db *sql.DB) (*Server, error) {
	s := new(Server)
	s.db = db
	s.addr = host
	s.password = password
	s.user = user
	return s, nil
}

func (s *Server) Run() {
	var err error
	s.listener, err = net.Listen("tcp", s.addr)
	if err != nil {

		return

	}
	defer s.listener.Close()

	log.Println("start server successful", s.addr)

	// begin to receive request
	s.running = true

	for s.running {
		conn, err := s.listener.Accept()
		if err != nil {
			log.Println(err)
			continue
		}

		go s.proxyHandle(conn, s.db)
	}
}
func (s *Server) Close() {
	s.running = false
	if s.listener != nil {
		s.listener.Close()
		s.db.Close()
	}
}
func (s *Server) IsRunning() bool {

	return s.running
}

func (s *Server) proxyHandle(conn net.Conn, db *sql.DB) {
	// close connection before exit
	defer conn.Close()

	log.Println("recv client", conn.RemoteAddr().String())

	// Create a connection with user root and an empty passowrd
	// We only an empty handler to handle command too
	siddonconn, _ := siddon.NewConn(conn, s.user, s.password, MysqlHandler{db: db})
	defer siddonconn.Close()
	for s.running {
		err := siddonconn.HandleCommand()
		if err != nil {
			log.Println(err)
			return
		}
	}
}
