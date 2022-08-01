package repmanv3

import (
	"context"
	"log"
	"testing"
)

func TestClient(t *testing.T) {
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

	res, err := client.GetCluster(context.Background(), &Cluster{
		Name: "masterslavehaproxy",
	})

	if err != nil {
		t.Fatalf("Could not GetCluster: %s", err)
	}

	log.Printf("resp: %v", res)
}
