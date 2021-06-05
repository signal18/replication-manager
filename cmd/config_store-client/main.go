package main

import (
	"context"
	"log"

	cs "github.com/signal18/replication-manager/config_store"
)

func main() {
	// generate a key one time to use for the secrets
	// key, err := cs.GenerateHexKey()
	// if err != nil {
	// 	log.Fatalf("Could not generate hex key: %s", err)
	// }
	key := "b00a4909fe6b7113c20ad8443cfe40075b817fe4351c4e287b44f1d69336edc7"
	log.Printf("Key: %s", key)

	// connect to the locally available config_store server
	csc := cs.NewConfigStore("127.0.0.1:7777", cs.Environment_NONE)
	csc.SetKeyFromHex(key)

	err := csc.ImportTOML("/etc/replication-manager/")
	if err != nil {
		log.Fatalf("Could not import TOML config: %s", err)
	}

	mySQLSection := csc.Section("mysql")

	props := make([]*cs.Property, 0)
	props = append(props, mySQLSection.NewProperty("client-test", "foo", "foo-2"))
	props = append(props, mySQLSection.NewProperty("client-test", "bar", "value1", "value20"))

	password, err := mySQLSection.NewSecret("cluster", "rootpassword", "somesecretpassword")
	if err != nil {
		log.Fatalf("Could not create secret")
	}
	props = append(props, password)

	log.Printf("password property: %v", password)

	ctx := context.Background()
	responses, err := csc.Store(ctx, props)
	if err != nil {
		log.Fatalf("Error storing: %s", err)
	}

	for _, r := range responses {
		log.Printf("Store response data: %v", r)
		if r.Secret {
			log.Printf("Property is secret: %s", r.GetValues())
		}
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
			Namespace: "bar-section",
		},
	})
	if err != nil {
		log.Printf("Error retrieving data: %s", err)
	}
	for _, r := range specificUnavailableNamespace {
		log.Printf("List bar-section data: %v", r)
	}

}
