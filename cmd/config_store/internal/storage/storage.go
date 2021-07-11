package storage

import (
	cs "github.com/signal18/replication-manager/config_store"
)

type ConfigStorage interface {
	Close() error
	Store(property *cs.Property) (*cs.Property, error)
	Search(query *cs.Query) ([]*cs.Property, error)
}
