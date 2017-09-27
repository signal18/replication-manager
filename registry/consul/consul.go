package consul

import (
	"github.com/signal18/replication-manager/registry"
)

func NewRegistry(opts ...registry.Option) registry.Registry {
	return registry.NewRegistry(opts...)
}
