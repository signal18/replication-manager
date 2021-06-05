package config_store

import (
	context "context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/spf13/viper"
	grpc "google.golang.org/grpc"
)

type ConfigStore struct {
	conn *grpc.ClientConn
	env  Environment
	key  []byte
}

func NewConfigStore(address string, env Environment) *ConfigStore {
	csc := &ConfigStore{
		env: env,
	}

	ctx := context.Background()
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())

	var err error
	csc.conn, err = grpc.DialContext(ctx,
		address,
		opts...,
	)
	if err != nil {
		log.Fatalf("Failed to dial: %v", err)
	}

	return csc
}

type ConfigSection struct {
	csc     *ConfigStore
	section []string
}

func (csc *ConfigStore) Section(name ...string) *ConfigSection {
	cs := &ConfigSection{
		csc:     csc,
		section: name,
	}

	return cs
}

func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func GenerateHexKey() (string, error) {
	key, err := GenerateKey()
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(key), nil
}

func (csc *ConfigStore) SetKey(key []byte) {
	csc.key = key
}

func (csc *ConfigStore) SetKeyFromHex(key string) error {
	binaryKey, err := hex.DecodeString(key)
	if err != nil {
		return err
	}
	csc.key = binaryKey
	return nil
}

func (csc *ConfigStore) NewProperty(section []string, namespace string, key string, values ...interface{}) *Property {
	return NewProperty(section, namespace, csc.env, key, values...)
}

func (csc *ConfigStore) NewSecret(section []string, namespace string, key string, values ...interface{}) (*Property, error) {
	if csc.key == nil {
		return nil, fmt.Errorf("config store key cannot be nil when using secrets")
	}

	p := NewSecret(section, namespace, csc.env, key, values...)

	return p, nil
}

func (cs *ConfigSection) NewProperty(namespace string, key string, values ...interface{}) *Property {
	return cs.csc.NewProperty(cs.section, namespace, key, values...)
}

func (cs *ConfigSection) NewSecret(namespace string, key string, values ...interface{}) (*Property, error) {
	return cs.csc.NewSecret(cs.section, namespace, key, values...)
}

func (csc *ConfigStore) Store(ctx context.Context, properties []*Property) ([]*Property, error) {
	client := NewConfigStoreClient(csc.conn)
	clientDeadline := time.Now().Add(time.Duration(30) * time.Second)
	ctx, _ = context.WithDeadline(ctx, clientDeadline)
	storeClient, err := client.Store(ctx)
	if err != nil {
		log.Printf("Cannot connect error: %v", err)
		return nil, err
	}

	var responses []*Property

	for _, p := range properties {
		if p.Secret {
			err := p.Encrypt(csc.key)
			if err != nil {
				log.Printf("Cannot encrypt: %s", err)
				return nil, err
			}
		}
		if err := storeClient.Send(p); err != nil {
			log.Printf("Error sending: %v", err)
			return nil, err
		}
		resp, err := storeClient.Recv()
		if err != nil {
			log.Printf("Error returned: %v", err)
			return nil, err
		}
		if resp.Secret {
			resp.Decrypt(csc.key)
			if err != nil {
				log.Printf("Cannot decrypt: %s", err)
				return nil, err
			}
		}
		responses = append(responses, resp)
	}

	err = storeClient.CloseSend()
	if err != nil {
		log.Fatalf("Error returned: %v", err)
		return nil, err
	}

	return responses, nil
}

func (cs *ConfigSection) Search(ctx context.Context, query *Query) ([]*Property, error) {
	query.Property.Section = cs.section

	return cs.csc.Search(ctx, query)
}

func (csc *ConfigStore) Search(ctx context.Context, query *Query) ([]*Property, error) {
	client := NewConfigStoreClient(csc.conn)
	listClient, err := client.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	var responses []*Property

	for {
		in, err := listClient.Recv()
		if err == io.EOF {
			break
		}

		if err != nil {
			log.Printf("Could not list: %s", err)
			return nil, err
		}

		if in.Secret {
			err = in.Decrypt(csc.key)
			if err != nil {
				log.Printf("Could not decrypt: %s", err)
				return nil, err
			}
		}

		responses = append(responses, in)
	}

	return responses, nil
}

func (csc *ConfigStore) ImportTOML(path string) error {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("toml")
	v.AddConfigPath(path)
	err := v.ReadInConfig()
	if err != nil {
		log.Fatalf("Could not read config: %s", err)
		return err
	}

	var props []*Property

	keys := v.AllKeys()
	for _, rawkey := range keys {
		log.Printf("key: %s", rawkey)

		var key string
		var section []string
		buf := strings.Split(rawkey, ".")
		if len(buf) == 2 {
			key = buf[len(buf)-1]
			section = buf[:len(buf)-1]
		}

		value := v.Get(rawkey)

		p := &Property{
			Key:     key,
			Section: section,
		}

		p.SetValues(value)

		log.Printf("p: %v", p)
		props = append(props, p)
	}

	log.Printf("found %d props", len(props))

	// push da tempo
	ctx := context.Background()
	stored, err := csc.Store(ctx, props)

	log.Printf("stored %d props", len(props))
	for _, p := range stored {
		log.Printf("stored: %v", p)
	}

	// TODO: we need to process the included files too

	return nil
}
