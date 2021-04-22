package river

import (
	"github.com/siddontang/go-mysql/schema"
)

type Rule struct {
	MSchema string `toml:"master_schema"`
	MTable  string `toml:"master_table"`
	CSchema string `toml:"cloud_schema" `
	CTable  string `toml:"cloud_table"`
	Parent  string `toml:"parent"`
	// Kafka need a topic destination
	KTopic      string `toml:"kafka_topic"`
	KPartitions int32  `toml:"kafka_partitions"`
	// Default, a MySQL table field name is mapped to Elasticsearch field name.
	// Sometimes, you want to use different name, e.g, the MySQL file name is title,
	// but in Elasticsearch, you want to name it my_title.
	FieldMapping map[string]string `toml:"field"`

	// MySQL table information
	TableInfo *schema.Table
}

func newDefaultRule(master_schema string, master_table string) *Rule {
	r := new(Rule)

	r.MSchema = master_schema
	r.MTable = master_table
	r.CSchema = master_schema
	r.CTable = master_table
	r.FieldMapping = make(map[string]string)

	return r
}

func (r *Rule) prepare() error {
	if r.FieldMapping == nil {
		r.FieldMapping = make(map[string]string)
	}

	if len(r.CSchema) == 0 {
		r.CSchema = r.MTable
	}

	if len(r.CTable) == 0 {
		r.CTable = r.MTable
	}

	return nil
}
