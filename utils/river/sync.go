package river

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/gwenn/yacr"
	"github.com/juju/errors"
	"github.com/siddontang/go-mysql-elasticsearch/elastic"
	"github.com/siddontang/go-mysql/canal"
	"github.com/siddontang/go-mysql/mysql"
	"github.com/siddontang/go-mysql/replication"
	"github.com/siddontang/go-mysql/schema"
	"github.com/siddontang/go/log"
)

const (
	syncInsertDoc = iota
	syncDeleteDoc
	syncUpdateDoc
)

const (
	fieldTypeList = "list"
	// for the mysql int type to es date type
	// set the [rule.field] created_time = ",date"
	fieldTypeDate = "date"
)

type posSaver struct {
	pos   mysql.Position
	force bool
}

type eventHandler struct {
	r *River
}

func (h *eventHandler) OnRotate(e *replication.RotateEvent) error {
	pos := mysql.Position{
		string(e.NextLogName),
		uint32(e.Position),
	}

	h.r.syncCh <- posSaver{pos, true}

	return h.r.ctx.Err()
}

func (h *eventHandler) OnDDL(nextPos mysql.Position, _ *replication.QueryEvent) error {
	h.r.syncCh <- posSaver{nextPos, true}
	return h.r.ctx.Err()
}

func (h *eventHandler) OnTableChanged(schema string, table string) error {
	log.Infof("Table change %s.%s", schema, table)
	return h.r.ctx.Err()
}

func (h *eventHandler) OnXID(nextPos mysql.Position) error {
	h.r.syncCh <- posSaver{nextPos, false}
	return h.r.ctx.Err()
}

func (h *eventHandler) OnRow(e *canal.RowsEvent) error {
	h.r.WaitForFlush()

	rule, ok := h.r.rules[ruleKey(e.Table.Schema, e.Table.Name)]
	// log.Warnf("Receive rows event in Do function and pass the rules   ")
	if !ok {
		return nil
	}

	var err error
	h.r.ProtectFromFlush()
	ddlBreack := h.r.beforebulktable != e.Table.Name || h.r.beforebulkschema != e.Table.Schema
	h.r.UnProtectFromFlush()

	if ddlBreack {

		log.Infof("Disable bulk mode breack in DDL")
		oldrule, ok := h.r.rules[ruleKey(h.r.beforebulkschema, h.r.beforebulktable)]
		if ok {
			if h.r.c.BatchMode == "SQL" || h.r.c.BatchMode == "F1" {
				log.Infof("Flushing insert breack in DDL %d", h.r.bulkidx)
				err = h.r.FlushMultiRowInsertBuffer(oldrule)
			}
		}
		h.r.ProtectFromFlush()
		h.r.bulkmode = false
		h.r.UnProtectFromFlush()

	}

	switch e.Action {
	case canal.InsertAction:
		h.r.ProtectFromFlush()
		if h.r.c.BatchMode == "SQL" || h.r.c.BatchMode == "F1" {
			h.r.buffered_inserts = append(h.r.buffered_inserts, e.Rows[0])
		} else {
			h.r.micro_transactions["insert_"+rule.CSchema+"_"+rule.CTable] = append(h.r.micro_transactions["insert_"+rule.CSchema+"_"+rule.CTable], e.Rows[0])
		}

		h.r.bulkidx++
		NeedFlush := h.r.bulkmode == true && h.r.bulkidx < h.r.c.BatchSize
		h.r.UnProtectFromFlush()

		if !(NeedFlush) {
			if h.r.c.BatchMode == "SQL" || h.r.c.BatchMode == "F1" {
				log.Infof("Flushing insert Buffer in Do Event %d", h.r.bulkidx)
				err = h.r.FlushMultiRowInsertBuffer(rule)
			}
			h.r.ProtectFromFlush()

			h.r.bulkmode = true
			h.r.UnProtectFromFlush()
		}
		h.r.ProtectFromFlush()
		h.r.beforewasinsert = 1
		h.r.UnProtectFromFlush()
	case canal.DeleteAction:
		h.r.ProtectFromFlush()
		h.r.bulkmode = false

		err = h.r.makeDeleteRequest(rule, e.Rows)
		h.r.beforewasinsert = 0
		h.r.UnProtectFromFlush()

	case canal.UpdateAction:
		h.r.bulkidx++
		h.r.micro_transactions["insert_"+rule.CSchema+"_"+rule.CTable] = append(h.r.micro_transactions["insert_"+rule.CSchema+"_"+rule.CTable], e.Rows[0])

		h.r.bulkmode = false
		err = h.r.makeUpdateRequest(rule, e.Rows)
		h.r.beforewasinsert = 0
	default:
		h.r.ProtectFromFlush()
		h.r.bulkmode = false
		h.r.UnProtectFromFlush()
		return errors.Errorf("invalid rows action %s", e.Action)
	}
	h.r.ProtectFromFlush()
	h.r.beforebulktable = e.Table.Name
	h.r.beforebulkschema = e.Table.Schema
	h.r.UnProtectFromFlush()
	if err != nil {
		return errors.Errorf("make %s ES request err %v", e.Action, err)
	}

	//if err := h.r.doBulk(reqs); err != nil {
	//	log.Errorf("do ES bulks err %v, stop", err)
	//	return canal.ErrHandleInterrupted
	//}

	return nil
}

func (h *eventHandler) OnGTID(gtid mysql.GTIDSet) error {
	return nil
}

func (h *eventHandler) OnPosSynced(pos mysql.Position, force bool) error {
	return nil
}

func (h *eventHandler) String() string {
	return "ESRiverEventHandler"
}

func (r *River) FlushMultiRowInsertBuffer(rule *Rule) error {
	r.flushmutex.Lock()
	r.makeInsertRequest(rule, r.buffered_inserts)
	r.buffered_inserts = nil
	r.bulkidx = 0
	r.flushmutex.Unlock()
	return nil
}

func (r *River) WaitForFlush() error {
	r.flushmutex.Lock()

	r.flushmutex.Unlock()
	return nil
}

func (r *River) ProtectFromFlush() error {
	r.flushmutex.Lock()

	return nil
}
func (r *River) UnProtectFromFlush() error {
	r.flushmutex.Unlock()
	return nil
}

func (r *River) FlushMicroTransaction(rule *Rule, table string) error {

	var newFile *os.File
	var err error
	newFile, err = os.Create(fmt.Sprintf("%s/INSERT_%s_%s.%09d", r.c.DumpPath, r.run_uuid, table, r.micro_transactions_id))
	if err != nil {
		log.Fatal(err)
	}
	w := yacr.NewWriter(newFile, '\t', true)

	for _, values := range r.micro_transactions["insert_"+table] {
		///	b := &bytes.Buffer{}
		//w := DefaultWriter(b)
		w.WriteRecord(values...)
		w.Flush()
		err := w.Err()

		if err != nil {
			log.Errorf("Unexpected error: %s\n", err)
		}

		//log.Warnf("%s", values)

	}
	newFile, err = os.Create(fmt.Sprintf("%s/DELETE_%s_%s.%09d", r.c.DumpPath, r.run_uuid, table, r.micro_transactions_id))
	if err != nil {
		log.Fatal(err)
	}
	w = yacr.NewWriter(newFile, '\t', true)
	for _, values := range r.micro_transactions["delete_"+table] {
		///	b := &bytes.Buffer{}
		//w := DefaultWriter(b)
		w.WriteRecord(values...)
		w.Flush()
		err := w.Err()

		if err != nil {
			log.Errorf("Unexpected error: %s\n", err)
		}

		//log.Warnf("%s", values)

	}

	r.micro_transactions["insert_"+table] = nil
	r.micro_transactions["delete_"+table] = nil
	r.bulkidx = 0
	newFile.Close()
	return nil
}

// for insert and delete
func (r *River) makeRequest(rule *Rule, action string, rows [][]interface{}) error {
	//	log.Warnf("Entering makeRequest  %s", rule.TableInfo.GetPKColumn(0).Name)

	var sql = ""
	var where = ""
	var c = rule.TableInfo.GetPKColumn(0)
	var rowsets []string
	var rowtodelete = make([]interface{}, 1)
	var ctrows = 0
	for _, values := range rows {

		id, err := r.getDocID(rule, values)
		if err != nil {
			return errors.Trace(err)
		}
		rowtodelete[0] = id
		if action == canal.DeleteAction {
			sql = sql + "( "

			if c.Type == schema.TYPE_STRING {
				sql = sql + fmt.Sprintf("%q", id)
			} else {
				sql = sql + id
			}
			sql = sql + ")"
			rowsets = append(rowsets, sql)

		}
		if action == canal.InsertAction {
			sql = sql + "( "
			sql = sql + r.makeInsertReqData(rule, values)
			sql = sql + ")"
			rowsets = append(rowsets, sql)

		}
		if action == canal.UpdateAction {
			//		log.Warnf("%d", ctrows)

			if ctrows == 0 {
				where = " WHERE " + rule.TableInfo.GetPKColumn(0).Name + "=" + id
			}
			if ctrows == 1 {
				sql = sql + r.makeUpdateReqData(rule, values)
			}
			rowsets = append(rowsets, sql)
		}

		ctrows++
	}

	if action == canal.DeleteAction {
		r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable] = append(r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable], rowtodelete)

		sql = "DELETE FROM " + rule.CSchema + "." + rule.CTable + " WHERE " + c.Name + " IN  "
	}
	if action == canal.InsertAction {
		sql = "INSERT INTO " + rule.CSchema + "." + rule.CTable + " VALUES "
	}
	if action == canal.UpdateAction {
		r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable] = append(r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable], rowtodelete)

		sql = "UPDATE   " + rule.CSchema + "." + rule.CTable + " SET "
	}

	sql = sql + strings.Join(rowsets, ",") + where
	if r.c.BatchMode == "SQL" {
		r.ExecuteDest(sql)
	}

	return nil
}

func GetPkStringF1(c *schema.TableColumn, id string) string {

	if c.Type == schema.TYPE_STRING {
		return fmt.Sprintf("%q", id)
	} else {
		return "NULL"
	}
}

func GetPkNumF1(c *schema.TableColumn, id string) string {

	if c.Type == schema.TYPE_NUMBER {
		return id
	} else {
		return "NULL"
	}
}

func (r *River) makeRequestF1(rule *Rule, action string, rows [][]interface{}) error {
	//	log.Warnf("Entering makeRequest  %s", rule.TableInfo.GetPKColumn(0).Name)

	var sql = ""
	var where = ""
	var c = rule.TableInfo.GetPKColumn(0)
	var rowsets []string
	var rowtodelete = make([]interface{}, 1)
	var ctrows = 0
	for _, values := range rows {
		sql = ""
		id, err := r.getDocID(rule, values)
		if err != nil {
			return errors.Trace(err)
		}
		rowtodelete[0] = id
		if action == canal.DeleteAction {
			sql = sql + "( "

			if c.Type == schema.TYPE_STRING {
				sql = sql + fmt.Sprintf("%q", id)
			} else {
				sql = sql + id
			}
			sql = sql + ")"
			rowsets = append(rowsets, sql)

		}
		if action == canal.InsertAction {
			sql = sql + "( CAST( CONV(LEFT(MD5(\"" + rule.MSchema + "." + rule.MTable + "\"), 16), 16, 10) AS UNSIGNED),COLUMN_CREATE( "
			sql = sql + r.makeInsertReqDataF1(rule, values)
			sql = sql + "), " + GetPkNumF1(c, id) + "," + GetPkStringF1(c, id) + " )"
			rowsets = append(rowsets, sql)

		}
		if action == canal.UpdateAction {
			//		log.Warnf("%d", ctrows)

			if ctrows == 0 {
				where = " WHERE " + rule.TableInfo.GetPKColumn(0).Name + "=" + id
			}
			if ctrows == 1 {
				sql = sql + r.makeUpdateReqData(rule, values)
			}
			rowsets = append(rowsets, sql)
		}

		ctrows++
	}

	if action == canal.DeleteAction {
		r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable] = append(r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable], rowtodelete)

		sql = "DELETE FROM " + rule.CSchema + "." + rule.CTable + " WHERE " + c.Name + " IN  "
	}
	if action == canal.InsertAction {
		sql = "INSERT INTO spdc.t(collection,content,inum,istr) VALUES "
	}
	if action == canal.UpdateAction {
		r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable] = append(r.micro_transactions["delete_"+rule.CSchema+"_"+rule.CTable], rowtodelete)

		sql = "UPDATE   " + rule.CSchema + "." + rule.CTable + " SET "
	}

	sql = sql + strings.Join(rowsets, ",") + where
	//log.Warnf("running:    %s", sql)
	r.ExecuteDest(sql)

	return nil
}

/*func (r *River) makeQuery(SQL strings) error {

}*/

func (r *River) makeInsertRequest(rule *Rule, rows [][]interface{}) error {
	//log.Warnf("makeInsertRequest:    %s", r.c.BatchMode)
	if r.c.BatchMode == "F1" {
		return r.makeRequestF1(rule, canal.InsertAction, rows)
	} else {
		return r.makeRequest(rule, canal.InsertAction, rows)
	}

}

func (r *River) makeDeleteRequest(rule *Rule, rows [][]interface{}) error {
	if r.c.BatchMode == "F1" {
		return nil
	} else {
		return r.makeRequest(rule, canal.DeleteAction, rows)
	}
}

func (r *River) makeUpdateRequest(rule *Rule, rows [][]interface{}) error {
	if len(rows)%2 != 0 {
		return errors.Errorf("invalid update rows event, must have 2x rows, but %d", len(rows))
	}
	if r.c.BatchMode == "F1" {
		return nil
	} else {
		return r.makeRequest(rule, canal.UpdateAction, rows)
	}
}

func (r *River) makeReqColumnData(col *schema.TableColumn, value interface{}) interface{} {
	switch col.Type {
	case schema.TYPE_ENUM:
		switch value := value.(type) {
		case int64:
			// for binlog, ENUM may be int64, but for dump, enum is string
			eNum := value - 1
			if eNum < 0 || eNum >= int64(len(col.EnumValues)) {
				// we insert invalid enum value before, so return empty
				log.Warnf("invalid binlog enum index %d, for enum %v", eNum, col.EnumValues)
				return ""
			}

			return col.EnumValues[eNum]
		}
	case schema.TYPE_SET:
		switch value := value.(type) {
		case int64:
			// for binlog, SET may be int64, but for dump, SET is string
			bitmask := value
			sets := make([]string, 0, len(col.SetValues))
			for i, s := range col.SetValues {
				if bitmask&int64(1<<uint(i)) > 0 {
					sets = append(sets, s)
				}
			}
			return strings.Join(sets, ",")
		}
	case schema.TYPE_STRING:
		switch value := value.(type) {
		case []byte:
			return string(value[:])
		}

	}
	return value
}

func (r *River) getFieldParts(k string, v string) (string, string, string) {
	composedField := strings.Split(v, ",")

	mysql := k
	remote := composedField[0]
	fieldType := ""

	if 0 == len(remote) {
		remote = mysql
	}
	if 2 == len(composedField) {
		fieldType = composedField[1]
	}

	return mysql, remote, fieldType
}

func (r *River) makeInsertReqData(rule *Rule, values []interface{}) string {
	//log.Warnf("  makeInsertReqData: ")
	var Data = make(map[string]interface{}, len(values))
	var sql = make([]string, 0, len(values))
	for i, c := range rule.TableInfo.Columns {
		mapped := false
		for k, v := range rule.FieldMapping {
			mysql, elastic, fieldType := r.getFieldParts(k, v)
			if mysql == c.Name {
				mapped = true
				v := r.makeReqColumnData(&c, values[i])
				if fieldType == fieldTypeList {
					if str, ok := v.(string); ok {
						Data[elastic] = strings.Split(str, ",")
					} else {
						Data[elastic] = v
					}
				} else {
					Data[elastic] = v

				}
			}
		}

		if mapped == false {
			//log.Warnf("  makeInsertReqData: C.NAME %s values :%s", c.Name, fmt.Sprint(values[i]))
			if c.Type == schema.TYPE_STRING {
				if values[i] == "NULL" || values[i] == nil {
					sql = append(sql, "NULL")
				} else {
					sql = append(sql, fmt.Sprintf("%q", values[i]))
				}
			} else {
				sql = append(sql, fmt.Sprint(values[i]))
			}
			//log.Warnf("  makeInsertReqData: C.NAME %s", r.makeReqColumnData(&c, values[i]))
			//Data[c.Name] = r.makeReqColumnData(&c, values[i])
		}
	}

	return strings.Join(sql, ",")
}

func (r *River) makeInsertReqDataF1(rule *Rule, values []interface{}) string {
	//log.Warnf("  makeInsertReqDataF1: ")
	var Data = make(map[string]interface{}, len(values))
	var sql = make([]string, 0, len(values))
	for i, c := range rule.TableInfo.Columns {
		mapped := false
		for k, v := range rule.FieldMapping {
			mysql, elastic, fieldType := r.getFieldParts(k, v)
			if mysql == c.Name {
				mapped = true
				v := r.makeReqColumnData(&c, values[i])
				if fieldType == fieldTypeList {
					if str, ok := v.(string); ok {
						Data[elastic] = strings.Split(str, ",")
					} else {
						Data[elastic] = v
					}
				} else {
					Data[elastic] = v

				}
			}
		}

		if mapped == false {
			//log.Warnf("  makeInsertReqData: C.NAME %s values :%s", c.Name, fmt.Sprint(values[i]))
			switch c.Type {
			case schema.TYPE_STRING:
				if values[i] == "NULL" || values[i] == nil {
					sql = append(sql, fmt.Sprintf("\"%s\", NULL ", c.Name))
				} else {
					sql = append(sql, fmt.Sprintf("\"%s\", %q ", c.Name, values[i]))
				}
			case schema.TYPE_NUMBER:
				if values[i] == "NULL" || values[i] == nil {
					sql = append(sql, fmt.Sprintf("\"%s\", NULL AS INTEGER", c.Name))
				} else {
					sql = append(sql, fmt.Sprintf("\"%s\" , %d AS INTEGER", c.Name, values[i]))
				}
			default:

				sql = append(sql, fmt.Sprintf("\"%s\", %q ", c.Name, values[i]))
			}

			//	log.Warnf("  makeInsertReqData: C.NAME %s", r.makeReqColumnData(&c, values[i]))
			//Data[c.Name] = r.makeReqColumnData(&c, values[i])
		}
	}

	return strings.Join(sql, ",")
}

func (r *River) makeUpdateReqData(rule *Rule, values []interface{}) string {

	var Data = make(map[string]interface{}, len(values))
	var sql = make([]string, 0, len(values))
	for i, c := range rule.TableInfo.Columns {
		mapped := false
		for k, v := range rule.FieldMapping {
			mysql, elastic, fieldType := r.getFieldParts(k, v)
			if mysql == c.Name {
				mapped = true
				v := r.makeReqColumnData(&c, values[i])
				if fieldType == fieldTypeList {
					if str, ok := v.(string); ok {
						Data[elastic] = strings.Split(str, ",")
					} else {
						Data[elastic] = v
					}
				} else {
					Data[elastic] = v

				}
			}
		}

		if mapped == false {

			if c.Type == schema.TYPE_STRING {

				if values[i] == "NULL" || values[i] == nil {
					sql = append(sql, fmt.Sprintf("%s=NULL", c.Name))
				} else {
					sql = append(sql, fmt.Sprintf("%s=%q", c.Name, values[i]))
				}
			} else {
				sql = append(sql, c.Name+"="+fmt.Sprint(values[i]))
			}
		}
	}

	return strings.Join(sql, ",")
}

// Get primary keys in one row and format them into a string
// PK must not be nil
func (r *River) getDocID(rule *Rule, row []interface{}) (string, error) {
	//	log.Warnf("  getDocID for rule:%s", rule.TableInfo)
	pks, err := rule.TableInfo.GetPKValues(row)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer

	sep := ""
	for i, value := range pks {
		if value == nil {
			return "", errors.Errorf("The %ds PK value is nil", i)
		}

		buf.WriteString(fmt.Sprintf("%s%v", sep, value))
		sep = ":"
	}

	return buf.String(), nil
}

func (r *River) getParentID(rule *Rule, row []interface{}, columnName string) (string, error) {
	index := rule.TableInfo.FindColumn(columnName)
	if index < 0 {
		return "", errors.Errorf("parent id not found %s(%s)", rule.TableInfo.Name, columnName)
	}

	return fmt.Sprint(row[index]), nil
}

func (r *River) doBulk(reqs []*elastic.BulkRequest) error {
	if len(reqs) == 0 {
		return nil
	}

	return nil
}
