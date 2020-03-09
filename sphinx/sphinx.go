package sphinx

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

type SphinxSQL struct {
	Connection *sqlx.DB
	User       string
	Password   string
	Port       string
	Host       string
}

func (sphinxql *SphinxSQL) Connect() error {
	SphinxConfig := mysql.Config{
		User:        sphinxql.User,
		Passwd:      sphinxql.Password,
		Net:         "tcp",
		Addr:        fmt.Sprintf("%s:%s", sphinxql.Host, sphinxql.Port),
		Timeout:     time.Second * 5,
		ReadTimeout: time.Second * 15,
	}

	var err error
	sphinxql.Connection, err = sqlx.Connect("mysql", SphinxConfig.FormatDSN())
	if err != nil {
		defer sphinxql.Connection.Close()
		return fmt.Errorf("Could not connect to SphinxQL (%s)", err)
	}
	return nil
}

func (sphinxql *SphinxSQL) GetVariables() (map[string]string, error) {
	type Variable struct {
		Variable_name string
		Value         string
	}

	vars := make(map[string]string)
	rows, err := sphinxql.Connection.Queryx("SHOW VARIABLES")
	if err != nil {
		return nil, errors.New("Could not get status variables")
	}
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return nil, errors.New("Could not get results from status scan")
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, nil
}

func (sphinxql *SphinxSQL) GetStatus() (map[string]string, error) {
	type Variable struct {
		Variable_name string
		Value         string
	}

	vars := make(map[string]string)
	rows, err := sphinxql.Connection.Queryx("SHOW STATUS")
	if err != nil {
		return nil, errors.New("Could not get status variables")
	}
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return nil, errors.New("Could not get results from status scan")
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, nil

}

func (sphinxql *SphinxSQL) GetVersion() string {
	var version string
	return version
}

func (sphinxql *SphinxSQL) GetIndexes() (map[string]string, error) {
	type Indexes struct {
		Index string
		Type  string
	}

	vars := make(map[string]string)
	rows, err := sphinxql.Connection.Queryx("SHOW TABLES")
	if err != nil {
		return nil, errors.New("Could not get status variables")
	}
	for rows.Next() {
		var v Indexes
		err := rows.Scan(&v.Index, &v.Type)
		if err != nil {
			return nil, errors.New("Could not get results from status scan")
		}
		vars[v.Index] = v.Type
	}
	return vars, nil
}
