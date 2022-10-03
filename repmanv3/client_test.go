package repmanv3

import (
	"context"
	"io"
	"log"
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"
)

func getClient(t *testing.T) (*Client, error) {
	conf := &ClientConfig{
		Address:            "localhost:10005",
		TLS:                true,
		InsecureSkipVerify: true,
		Auth: &LoginCreds{
			Username: "admin",
			Password: "repman",
		},
	}
	client, err := NewClient(context.Background(), conf)
	if err != nil {
		t.Fatalf("Could not create new Client: %s", err)
	}

	return client, err
}

func TestClient(t *testing.T) {
	client, err := getClient(t)
	if err != nil {
		t.Fatalf("Could not get test client: %s", err)
	}

	res, err := client.GetCluster(context.Background(), &Cluster{
		Name: "masterslavehaproxy",
	})

	if err != nil {
		t.Fatalf("Could not GetCluster: %s", err)
	}

	log.Printf("resp: %v", res)
}

func Test_ListClusters(t *testing.T) {
	client, err := getClient(t)
	if err != nil {
		t.Fatalf("Could not get test client: %s", err)
	}

	stream, err := client.ListClusters(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("Could not ListClusters: %s", err)
	}

	for {
		cluster, err := stream.Recv()
		if err == io.EOF {
			break
		}

		if err != nil {
			t.Fatalf("Error while receiving incoming ListClusters: %s", err)
		}

		t.Logf("cluster: %s", cluster.Name)
		t.Logf("cluster: %v", cluster.Conf)
	}
}
