package river

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/juju/errors"
	"github.com/siddontang/go-mysql/canal"
	"github.com/siddontang/go-mysql/mysql"
	"github.com/siddontang/go-mysql/schema"
	log "github.com/sirupsen/logrus"
)

type River struct {
	c                     *Config
	canal                 *canal.Canal
	rules                 map[string]*Rule
	st                    *stat
	quit                  chan struct{}
	wg                    sync.WaitGroup
	bulkmode              bool
	buffered_inserts      [][]interface{}
	micro_transactions    map[string][][]interface{}
	micro_transactions_id int64
	buffered_deletes      [][]interface{}
	bulkidx               int64
	beforebulktable       string
	beforebulkschema      string
	beforewasinsert       int64
	dumpdone              bool
	ticker                *time.Ticker
	flushmutex            *sync.Mutex
	run_uuid              string
	bufdestsql            []string
	// slaveconn             *client.Conn
	slavepool *sql.DB
	syncCh    chan interface{}
	ctx       context.Context
}

func NewRiver(c *Config) (*River, error) {
	r := new(River)

	r.c = c
	r.bulkmode = true
	r.bulkidx = 0
	r.beforewasinsert = 0
	r.dumpdone = !r.c.DumpInit
	r.quit = make(chan struct{})
	r.rules = make(map[string]*Rule)
	r.micro_transactions = make(map[string][][]interface{})
	r.micro_transactions_id = 0

	r.syncCh = make(chan interface{}, 4096)
	r.rules = make(map[string]*Rule)

	var err error

	var dsn = r.c.SlaveUser + ":" + r.c.SlavePassword + "@tcp(" + r.c.SlaveHost + ")/"
	r.slavepool, err = sql.Open("mysql", dsn) // this does not really open a new connection

	if err != nil {
		log.Fatalf("Error on initializing slave database connection: %s", err.Error())
	}
	r.slavepool.SetMaxOpenConns(4)
	r.slavepool.SetMaxIdleConns(1)

	//  defer r.slavepool.Close()

	if r.c.DumpInit {
		err = os.Remove(r.c.DumpPath + "/master.info")
	}

	if err = r.newCanal(); err != nil {
		return nil, errors.Trace(err)
	}

	if err = r.prepareRule(); err != nil {
		return nil, errors.Trace(err)
	}

	if err = r.prepareCanal(); err != nil {
		return nil, errors.Trace(err)
	}

	// We must use binlog full row image
	if err = r.canal.CheckBinlogRowImage("FULL"); err != nil {
		return nil, errors.Trace(err)
	}

	r.st = &stat{r: r}

	go r.st.Run(r.c.StatAddr)
	return r, nil
}

func (r *River) newCanal() error {
	cfg := canal.NewDefaultConfig()
	cfg.Addr = r.c.MyHost
	cfg.User = r.c.MyUser
	cfg.Password = r.c.MyPassword
	cfg.Flavor = r.c.MyFlavor
	//cfg.DataDir = r.c.DumpPath
	cfg.ServerID = r.c.DumpServerID
	cfg.Dump.ExecutionPath = r.c.DumpExec
	cfg.Dump.DiscardErr = false
	cfg.Dump.SkipMasterData = false

	//	cfg. StatAddr = "127.0.0.1:12800"

	// return errors.Errorf("%s,%s,%s", cfg.Addr, cfg.User, cfg.Password)
	var err error
	r.canal, err = canal.NewCanal(cfg)
	return errors.Trace(err)
}

func (r *River) prepareCanal() error {
	var db string
	dbs := map[string]struct{}{}
	tables := make([]string, 0, len(r.rules))
	for _, rule := range r.rules {
		db = rule.MSchema
		dbs[rule.MSchema] = struct{}{}
		tables = append(tables, rule.MTable)
	}

	if len(dbs) == 1 {
		// one db, we can shrink using table
		r.canal.AddDumpTables(db, tables...)
	} else {
		// many dbs, can only assign databases to dump
		keys := make([]string, 0, len(dbs))
		for key, _ := range dbs {
			keys = append(keys, key)
		}

		r.canal.AddDumpDatabases(keys...)

	}

	r.canal.SetEventHandler(&eventHandler{r})

	return nil
}

func (r *River) newRule(schema, table string) error {
	key := ruleKey(schema, table)

	if _, ok := r.rules[key]; ok {
		return errors.Errorf("duplicate source %s, %s defined in config", schema, table)
	}

	r.rules[key] = newDefaultRule(schema, table)
	return nil
}

func (r *River) parseSource() (map[string][]string, error) {
	wildTables := make(map[string][]string, len(r.c.Sources))

	// first, check sources
	for _, s := range r.c.Sources {
		for _, table := range s.Tables {
			if len(s.Schema) == 0 {
				return nil, errors.Errorf("empty schema not allowed for source")
			}

			if regexp.QuoteMeta(table) != table {
				if _, ok := wildTables[ruleKey(s.Schema, table)]; ok {
					return nil, errors.Errorf("duplicate wildcard table defined for %s.%s", s.Schema, table)
				}

				tables := []string{}

				sql := fmt.Sprintf(`SELECT table_name FROM information_schema.tables WHERE
                    table_name RLIKE "%s" AND table_schema = "%s";`, table, s.Schema)

				res, err := r.canal.Execute(sql)
				if err != nil {
					return nil, errors.Trace(err)
				}

				for i := 0; i < res.Resultset.RowNumber(); i++ {
					f, _ := res.GetString(i, 0)
					err := r.newRule(s.Schema, f)
					if err != nil {
						return nil, errors.Trace(err)
					}

					tables = append(tables, f)
				}

				wildTables[ruleKey(s.Schema, table)] = tables
			} else {
				err := r.newRule(s.Schema, table)
				if err != nil {
					return nil, errors.Trace(err)
				}
			}
		}
	}

	if len(r.rules) == 0 {
		return nil, errors.Errorf("no source data defined")
	}

	return wildTables, nil
}

func (r *River) DumpIndexes(idx *Index) error {
	var rr *mysql.Result
	var err error

	//var e canal.RowsEvent

	e := new(canal.RowsEvent)

	rr, err = r.canal.Execute(idx.Sql)
	r.handleError(err)
	e.Table.Name = idx.CloudTable
	e.Table.Schema = idx.CloudSchema
	e.Rows = rr.Resultset.Values
	//r.canal.r RegRowsEventHandler(h)
	//r.canal.rsDo(e)
	//for _, h := range r.canal.rsHandlers {
	//	err = h.Do(e)
	//}
	return nil
}

func (r *River) prepareRule() error {
	wildtables, err := r.parseSource()
	if err != nil {
		log.Infof("Erreur in parseSource %s ", err)
		return errors.Trace(err)
	}

	if r.c.Rules != nil {
		// then, set custom mapping rule
		for _, rule := range r.c.Rules {
			if len(rule.MSchema) == 0 {
				return errors.Errorf("empty schema not allowed for rule")
			}

			if regexp.QuoteMeta(rule.MTable) != rule.MTable {
				//wildcard table
				tables, ok := wildtables[ruleKey(rule.MSchema, rule.MTable)]
				if !ok {
					return errors.Errorf("wildcard table for %s.%s is not defined in source", rule.MSchema, rule.MTable)
				}

				if len(rule.CSchema) == 0 {
					return errors.Errorf("wildcard table rule %s.%s must have a index, can not empty", rule.MSchema, rule.MTable)
				}

				rule.prepare()

				for _, table := range tables {
					rr := r.rules[ruleKey(rule.MSchema, table)]
					rr.CSchema = rule.CSchema
					rr.CTable = rule.CTable
					rr.Parent = rule.Parent
					rr.FieldMapping = rule.FieldMapping
				}
			} else {
				key := ruleKey(rule.MSchema, rule.MTable)
				log.Infof("Filtering :%s", key)
				if _, ok := r.rules[key]; !ok {
					return errors.Errorf("rule %s, %s not defined in source", rule.MSchema, rule.MTable)
				}
				rule.prepare()
				r.rules[key] = rule
			}
		}
	}

	for _, rule := range r.rules {
		if rule.TableInfo, err = r.canal.GetTable(rule.MSchema, rule.MTable); err != nil {
			return errors.Trace(err)
		}

		// table must have a PK for one column, multi columns may be supported later.

		if len(rule.TableInfo.PKColumns) != 1 {
			return errors.Errorf("%s.%s must have a PK for a column", rule.MSchema, rule.MTable)
		}
	}

	return nil
}

func ruleKey(schema string, table string) string {
	return fmt.Sprintf("%s:%s", schema, table)
}

func (r *River) Run() error {

	r.flushmutex = &sync.Mutex{}
	u, err := exec.Command("uuidgen").Output()
	if err != nil {
		log.Fatal(err)
		return err
	}
	r.run_uuid = fmt.Sprintf("%s", u)

	if r.c.DumpInit {
		log.Infof("Starting River in Init Mode")
		if r.c.BatchMode == "F1" {
			log.Infof("Deleting Collection")

			for _, rule := range r.c.Rules {
				log.Infof("Deleting collection %s.%s", rule.MSchema, rule.MTable)
				sql := "delete from spdc.t where collection=CAST(CONV(LEFT(MD5(\"" + rule.MSchema + "." + rule.MTable + "\"), 16), 16, 10) AS UNSIGNED)"
				//	r.ExecuteDest(sql)
				sql = "CREATE DATABASE IF NOT EXISTS " + rule.MSchema
				r.ExecuteDest(sql)
				sql = "CREATE OR REPLACE VIEW  " + rule.MSchema + "." + rule.MTable + " AS SELECT "
				var vcols []string
				for _, c := range rule.TableInfo.Columns {
					if c.Name == rule.TableInfo.GetPKColumn(0).Name {

						if c.Type == schema.TYPE_NUMBER {
							vcols = append(vcols, "inum  AS "+c.Name)
						} else {
							vcols = append(vcols, "istr AS "+c.Name)
						}
					} else {

						if c.Type == schema.TYPE_NUMBER {
							vcols = append(vcols, "COLUMN_GET(content,\""+c.Name+"\" AS INTEGER) AS "+c.Name)
						} else {
							vcols = append(vcols, "COLUMN_GET(content,\""+c.Name+"\" AS CHAR) AS "+c.Name)

						}

					}
				}
				sql = sql + strings.Join(vcols, ",") + " from spdc.t_ro where  collection=CAST( CONV(LEFT(MD5(\"" + rule.MSchema + "." + rule.MTable + "\"), 16), 16, 10) AS UNSIGNED)"
				//	r.ExecuteDest(sql)
			}

		}
	} else {
		log.Infof("Starting River in Delta Mode")
	}

	go func() {
		<-r.canal.WaitDumpDone()
		log.Infof("Dump Finished for River")
		rule, ok := r.rules[ruleKey(r.beforebulkschema, r.beforebulktable)]
		if ok {
			r.FlushMultiRowInsertBuffer(rule)
			r.dumpdone = true

		}
	}()
	/*	if len(r.canal.master.Name) > 0 && r.canal.master.Position > 0 {
		fmt.Println("=you wan't to skip dump")
	}*/

	r.ticker = time.NewTicker(time.Millisecond * time.Duration(r.c.BatchTimeOut))

	go func() {
		for t := range r.ticker.C {

			if r.c.BatchMode == "CSV" {
				as_data := 0
				for _, rule := range r.rules {
					if r.micro_transactions["insert_"+rule.CSchema+"_"+rule.CTable] != nil {

						err := r.FlushMicroTransaction(rule, rule.CSchema+"_"+rule.CTable)
						if err != nil {
							log.Fatal(err)
							return
						}
						as_data = 1
					}
				}
				if as_data == 1 {
					r.TarGz(fmt.Sprintf(r.c.DumpPath+"/%09d.tar.gz", r.micro_transactions_id), r.c.DumpPath, fmt.Sprintf("%09d", r.micro_transactions_id))
					r.micro_transactions_id++
				}

			} else {
				if r.buffered_inserts != nil {
					log.Infof("Forcing Flush at %q", t.Format("15:04:05.99999999"))

					rule, ok := r.rules[ruleKey(r.beforebulkschema, r.beforebulktable)]
					if ok {
						/*	r.ProtectFromFlush()
							 r.bulkmode = false
							r.UnProtectFromFlush()
						*/

						r.FlushMultiRowInsertBuffer(rule)

					}

				}
			}
		}
	}()

	if err := r.canal.Run(); err != nil {
		log.Errorf("start canal err %v", err)

		return errors.Trace(err)
	}

	return nil
}
func (r *River) handleError(_e error) {
	if _e != nil {
		log.Fatal(_e)
	}
}

func (r *River) IterDirectory(dirPath string, tw *tar.Writer, id string) {
	dir, err := os.Open(dirPath)
	r.handleError(err)
	defer dir.Close()
	fis, err := dir.Readdir(0)
	r.handleError(err)
	for _, fi := range fis {
		if strings.Contains(fi.Name(), "."+id) {
			curPath := dirPath + "/" + fi.Name()

			fmt.Printf("adding... %s\n", curPath)
			r.TarGzWrite(curPath, tw, fi)
			err = os.Remove(curPath)
			r.handleError(err)
		}
	}
}

func (r *River) TarGz(outFilePath string, inPath string, id string) {
	// file write
	fw, err := os.Create(outFilePath)
	r.handleError(err)
	defer fw.Close()

	// gzip write
	gw := gzip.NewWriter(fw)
	defer gw.Close()

	// tar write
	tw := tar.NewWriter(gw)
	defer tw.Close()

	r.IterDirectory(inPath, tw, id)

	fmt.Println("tar.gz ok")
}

func (r *River) TarGzWrite(_path string, tw *tar.Writer, fi os.FileInfo) {
	fr, err := os.Open(_path)
	r.handleError(err)
	defer fr.Close()

	h := new(tar.Header)
	h.Name = fi.Name()
	h.Size = fi.Size()
	h.Mode = int64(fi.Mode())
	h.ModTime = fi.ModTime()

	err = tw.WriteHeader(h)
	r.handleError(err)

	_, err = io.Copy(tw, fr)
	r.handleError(err)
}

func (r *River) ExecuteDest(cmd string, args ...interface{}) (rr *sql.Rows, err error) {

	retryNum := 3
	//return
	for i := 0; i < retryNum; i++ {
		_, err = r.slavepool.Exec(cmd)
		if err != nil {
			log.Fatal(err)

			continue

		} else {

			return
		}
	}
	return
}

/*

func (r *River) ExecuteDest(cmd string, args ...interface{}) (rr *mysql.Result, err error) {

	retryNum := 3


	for i := 0; i < retryNum; i++ {
		if r.slaveconn == nil {
			r.slaveconn, err = client.Connect(r.c.SlaveHost, r.c.SlaveUser, r.c.SlavePassword, "")
			if err != nil {
				log.Errorf("%s", err.Error())
				return nil, errors.Trace(err)
			}
		}
		//log.Errorf("%s", cmd)
		rr, err = r.slaveconn.Execute(cmd, args...)
		if err != nil && err != mysql.ErrBadConn {
			if len(cmd) > 199 {
				log.Errorf("%s %s", err.Error(), cmd[:200])
			} else {
				log.Errorf("%s %s", err.Error(), cmd)

			}

			continue
		} else if err == mysql.ErrBadConn {
			log.Errorf("%s", err.Error())
			r.slaveconn.Close()
			r.slaveconn = nil
			continue
		} else {

			return
		}
	}
	return
}

*/

func (r *River) Close() {
	r.ticker.Stop()
	fmt.Println("Ticker stopped")
	log.Infof("closing river")
	close(r.quit)

	r.canal.Close()

	r.wg.Wait()
	//r.slaveconn.Close()
	//r.slaveconn = nil
}
