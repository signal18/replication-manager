package main

import (
	"context"
	"log"

	cs "github.com/signal18/replication-manager/config_store"
)

func main() {
	// connect to the locally available config_store server

	csc := cs.NewConfigStore("127.0.0.1:7777", cs.Environment_NONE)

	err := csc.ImportTOML("/etc/replication-manager/")
	if err != nil {
		log.Fatalf("Could not import TOML config: %s", err)
	}

	props := make([]*cs.Property, 0)
	props = append(props, csc.NewStringProperty([]string{"foo", "baz"}, "client-test", "foo", "foo-2"))
	props = append(props, csc.NewStringProperty([]string{}, "client-test", "bar", "bar"))

	// for checking the error on the server
	// props = append(props, &cs.Property{
	// 	Value: "foo-2",
	// 	Store: "client-test",
	// })

	ctx := context.Background()
	responses, err := csc.Store(ctx, props)
	if err != nil {
		log.Fatalf("Error storing: %s", err)
	}

	for _, r := range responses {
		log.Printf("Store response data: %v", r)
	}

	// list the available properties
	available, err := csc.Search(ctx, &cs.Query{})
	if err != nil {
		log.Printf("Error retrieving data: %s", err)
	}
	for _, r := range available {
		log.Printf("List response data: %v", r)
	}

	specificKey, err := csc.Search(ctx, &cs.Query{
		Property: &cs.Property{
			Key:       "foo",
			Namespace: "client-test",
		},
	})
	if err != nil {
		log.Printf("Error retrieving data: %s", err)
	}
	for _, r := range specificKey {
		log.Printf("List specificKey data: %v", r)
	}

	specificNamespace, err := csc.Search(ctx, &cs.Query{
		Property: &cs.Property{
			Namespace: "client-test",
		},
	})
	if err != nil {
		log.Printf("Error retrieving data: %s", err)
	}
	for _, r := range specificNamespace {
		log.Printf("List specificNamespace data: %v", r)
	}

	specificUnavailableNamespace, err := csc.Search(ctx, &cs.Query{
		Property: &cs.Property{
			Namespace: "foo",
		},
	})
	if err != nil {
		log.Printf("Error retrieving data: %s", err)
	}
	for _, r := range specificUnavailableNamespace {
		log.Printf("List foo data: %v", r)
	}

}
