package repmanv3

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type ClientConfig struct {
	Address            string
	TLS                bool
	InsecureSkipVerify bool
	Auth               *LoginCreds
}

type Client struct {
	ClusterPublicServiceClient
	ClusterServiceClient
}

func NewConnection(ctx context.Context, conf *ClientConfig) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if conf.Auth != nil {
		opts = append(opts, grpc.WithPerRPCCredentials(
			conf.Auth,
		))
	}

	if conf.TLS {
		addr, err := url.Parse("//" + conf.Address)
		if err != nil {
			return nil, fmt.Errorf("error parsing Cluster Address: %s", err)
		}

		creds := credentials.NewTLS(&tls.Config{
			ServerName:         addr.Hostname(),
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: conf.InsecureSkipVerify,
		})

		opts = append(opts,
			grpc.WithTransportCredentials(creds),
		)
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	return grpc.DialContext(ctx, conf.Address, opts...)
}

func NewClient(ctx context.Context, conf *ClientConfig) (*Client, error) {
	c := &Client{}
	conn, err := NewConnection(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("Could not create new connection: %s", err)
	}
	c.ClusterPublicServiceClient = NewClusterPublicServiceClient(conn)
	c.ClusterServiceClient = NewClusterServiceClient(conn)

	return c, nil
}
