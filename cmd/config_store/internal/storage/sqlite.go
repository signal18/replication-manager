package storage

import (
	"errors"
	"fmt"
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
		"key, value, section, namespace, " +
		"environment, revision, version, " +
		"created" +
		") VALUES (" +
		"?, ?, ?, ?, " +
		"?, ?, ?, " +
		"?" +
		")"
)

func (st *SQLiteStorage) Store(property *cs.Property) (*cs.Property, error) {
	log.Printf("Query: \n%s", storeProperty)

	property.Created = timestamppb.Now()

	_, err := st.db.Exec(storeProperty,
		property.Key,
		property.DatabaseValue(),
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

	for rows.Next() {
		buf := make(map[string]interface{})
		err = rows.MapScan(buf)
		if err != nil {
			return
		}

		p := &cs.Property{}
		err = p.Scan(buf)
		if err != nil {
			return
		}

		results = append(results, p)
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

	if !q.IgnoreValue {
		if q.Property.DatabaseValue() != "" {
			queries = append(queries, "value = ?")
			values = append(values, q.Property.DatabaseValue())
		}
	}

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

	if q.Limit != 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}

	return query, values
}
