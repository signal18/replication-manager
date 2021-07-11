package main

import (
	"log"

	"github.com/signal18/replication-manager/cmd/config_store/internal/server"
	"github.com/signal18/replication-manager/cmd/config_store/internal/storage"
)

func main() {
	sconf := server.Config{
		ListenAddressForgRPC: "127.0.0.1:7777",
	}

	st, err := storage.NewSQLiteStorage("/tmp/test.sqlite")
	if err != nil {
		log.Fatalf("Error creating SQLite Storage: %s", err)
	}
	defer st.Close()

	s := server.NewServer(sconf, st)

	go func(s *server.Server) {
		err := s.StartGRPCServer()
		if err != nil {
			log.Fatalf("Failed to start gRPC server: %s", err)
		}
	}(s)

	// the gRPC server is up and running
	if <-s.Up {
		log.Println("We are up and running")

		// enter an infinite loop as else the program would exit since our only
		// blocking call is inside a Go routine
		select {}
	}
}
