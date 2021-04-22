package storage

import (
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	cs "github.com/signal18/replication-manager/config_store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SQLiteStorage struct {
	path string
	db   *sqlx.DB
}

func NewSQLiteStorage(path string) (*SQLiteStorage, error) {
	st := &SQLiteStorage{
		path: path,
	}

	if err := st.open(); err != nil {
		return nil, err
	}

	return st, nil
}

func (st *SQLiteStorage) Close() error {
	return st.db.Close()
}

func (st *SQLiteStorage) open() error {
	var err error

	create := false
	if _, err := os.Stat(st.path); os.IsNotExist(err) {
		create = true
	}

	st.db, err = sqlx.Open("sqlite3", st.path)
	if err != nil {
		return err
	}

	if create {
		if err := st.db.Ping(); err != nil {
			return err
		}

		if err := st.create(); err != nil {
			return err
		}
	}

	return st.db.Ping()
}

func (st *SQLiteStorage) create() error {
	schema := `CREATE TABLE properties (
		key text,
		value text null,
		value_order int default 0, 
		value_type int default 0,
		section text null,
		namespace text default "default",
		environment int default 0,
		revision int default 0,
		version string null,
		created TEXT default CURRENT_TIMESTAMP,
		deleted TEXT null
	);`

	result, err := st.db.Exec(schema)
	if err != nil {
		return err
	}

	log.Printf("Create result: %v", result)
	return nil
}

var (
	storeProperty = "INSERT INTO properties (" +
		"key, " +
		"value, " +
		"value_order, " +
		"value_type, " +
		"section, " +
		"namespace, " +
		"environment, " +
		"revision, " +
		"version, " +
		"created" +
		") VALUES (" +
		"?, ?, ?, ?, ?, " +
		"?, ?, ?, ?, ?" +
		")"
)

func (st *SQLiteStorage) Store(property *cs.Property) (*cs.Property, error) {
	log.Printf("Query: \n%s", storeProperty)

	property.Created = timestamppb.Now()

	for order, value := range property.Values {
		_, err := st.db.Exec(storeProperty,
			property.Key,
			value.Data,
			order,
			value.Type,
			strings.Join(property.Section, cs.RecordSeperator),
			property.Namespace,
			property.Environment,
			property.Revision,
			property.Version,
			property.Created.AsTime().Format(time.RFC3339Nano),
		)
		if err != nil {
			return nil, err
		}
	}

	return property, nil
}

var (
	ErrNoRowsFound = errors.New("no results")
)

func (st *SQLiteStorage) Search(query *cs.Query) (results []*cs.Property, err error) {
	results = make([]*cs.Property, 0)

	sql, values := st.getSQLQuery(query)
	log.Printf("SQL Query: %s\nQuery: %v", sql, query)
	rows, err := st.db.Queryx(sql, values...)
	if err != nil {
		return
	}

	if rows == nil {
		return results, ErrNoRowsFound
	}

	var lastKey string
	var p *cs.Property

	for rows.Next() {
		buf := make(map[string]interface{})
		err = rows.MapScan(buf)
		if err != nil {
			return
		}

		// if the lastKey doesn't match the incoming key
		// reset the Property as it doesn't have additional Values
		if key, ok := buf["key"]; ok {
			if key != nil {
				if lastKey != key.(string) {
					if lastKey != "" {
						results = append(results, p)
					}
					p = &cs.Property{}
				}
			}
		}
		err = p.Scan(buf)
		if err != nil {
			return
		}

		lastKey = p.Key
	}

	// because of the way we loop over the set we have to add the final one
	// to the actual results
	if p != nil {
		results = append(results, p)
	}

	if len(results) > int(query.Limit) {
		return results[:query.Limit], nil
	}

	return
}

func (st *SQLiteStorage) getSQLQuery(q *cs.Query) (query string, values []interface{}) {
	basequery := "SELECT * FROM properties"

	if q.Property == nil {
		query = basequery
		return
	}

	basequery += " WHERE "
	queries := make([]string, 0)

	if q.Property.Key != "" {
		queries = append(queries, "key = ?")
		values = append(values, q.Property.Key)
	}

	// TODO: reimplement
	// if !q.IgnoreValue {
	// 	if q.Property.DatabaseValue() != "" {
	// 		queries = append(queries, "value = ?")
	// 		values = append(values, q.Property.DatabaseValue())
	// 	}
	// }

	if q.Property.Namespace != "" {
		queries = append(queries, "namespace = ?")
		values = append(values, q.Property.Namespace)
	}

	if q.Property.Environment != 0 {
		queries = append(queries, "environment = ?")
		values = append(values, q.Property.Environment)
	}

	if q.Property.Revision != 0 {
		queries = append(queries, "revision = ?")
		values = append(values, q.Property.Revision)
	}

	if q.Property.Version != "" {
		queries = append(queries, "version = ?")
		values = append(values, q.Property.Version)
	}

	query = basequery + strings.Join(queries, " AND ")

	query += " ORDER BY revision DESC"

	// normally we'd do a LIMIT here but we do that inside the search :)

	return query, values
}
