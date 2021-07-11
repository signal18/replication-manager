package server

import (
	"fmt"
	"io"
	"log"
	"net"

	"github.com/signal18/replication-manager/cmd/config_store/internal/storage"
	cs "github.com/signal18/replication-manager/config_store"
	"google.golang.org/grpc"
)

type Server struct {
	cs.ConfigStoreServer

	st storage.ConfigStorage

	grpcServer *grpc.Server
	conf       Config
	Up         chan bool
}

type Config struct {
	ListenAddressForgRPC string
}

func NewServer(conf Config, storage storage.ConfigStorage) *Server {
	s := Server{
		conf: conf,
		st:   storage,
	}

	s.Up = make(chan bool)
	return &s
}

func (s *Server) StartGRPCServer() error {
	lis, err := net.Listen("tcp", s.conf.ListenAddressForgRPC)
	if err != nil {
		return err
	}

	// create a gRPC server object
	s.grpcServer = grpc.NewServer()

	cs.RegisterConfigStoreServer(s.grpcServer, s)

	s.Up <- true
	log.Printf("starting HTTP/2 gRPC server, listening on: %s", lis.Addr().String())
	if err := s.grpcServer.Serve(lis); err != nil {
		return err
	}

	return nil
}

func (s *Server) Store(stream cs.ConfigStore_StoreServer) error {
	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}

		if in == nil {
			return fmt.Errorf("empty object sent")
		}

		log.Printf("Received property: %v", in)

		err = in.Validate()
		if err != nil {
			log.Printf("Error on validation: %s", err)
			return err
		}

		// check if the Store is set, if not set to default
		if in.Namespace == "" {
			in.Namespace = "default"
		}

		find, err := s.st.Search(&cs.Query{
			Property:    in,
			Limit:       1,
			IgnoreValue: true,
		})

		if err != nil && err != storage.ErrNoRowsFound {
			log.Printf("Error on searching for existing property: %s", err)
			return err
		}

		if len(find) == 1 {
			found := find[0]
			if found.Key == in.Key && found.Namespace == in.Namespace && found.Environment == in.Environment {

				if cs.ValuesEqual(found.Values, in.Values) {
					log.Printf("Property did not change: %v", in)
					stream.Send(found)
					continue
				}
				log.Printf("Updating existing property: %v with %v", found, in)
				found.Values = in.Values
				found.Revision++
				in = found
			}
		}

		out, err := s.st.Store(in)
		if err != nil {
			log.Printf("Error on storing: %s", err)
			return err
		}

		stream.Send(out)
	}
}

func (s *Server) Search(query *cs.Query, stream cs.ConfigStore_SearchServer) error {
	properties, err := s.st.Search(query)
	if err != nil {
		return err
	}

	for _, p := range properties {
		err := stream.Send(p)
		if err != nil {
			log.Printf("Error sending: %s", err)
			return err
		}
	}

	return nil
}
