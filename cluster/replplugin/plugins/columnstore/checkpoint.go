// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/go-mysql-org/go-mysql/schema"
	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// The checkpoints (#2015): what the plugin captured of a table during a period, as two
// files in LOAD DATA's format, the keys to delete and the rows to load, packed into a
// tar.gz with its manifest; replication-manager deletes the keys then loads the rows. In a
// period a row inserted then deleted cancels out, an update is a delete of its old key and
// an insert of its new row, and the keys of the loaded rows are deleted too, so a
// checkpoint applied twice gives the same table.

// row is a row in LOAD DATA's terms: nil, string or bytes per column.
type row []interface{}

type tableBuf struct {
	t       *schema.Table
	key     []int // the columns a row is found by; nil: keyless (keyErr set when updates and deletes cannot be applied)
	keyErr  error
	inserts map[string]row // by key
	order   []string       // keys in the order of their first insert
	deletes map[string]row // key values, by key
	delOrd  []string
	list    []row // keyless: the inserted rows
	dels    []row // keyless: the deleted rows' key values
	loads   []row // the restore's rows: into a table the restore created, nothing to delete
}

func newTableBuf(t *schema.Table) *tableBuf {
	b := &tableBuf{t: t, inserts: map[string]row{}, deletes: map[string]row{}}
	key, err := keyColumns(t)
	if err != nil {
		b.keyErr = err
	} else if len(t.PKColumns) > 0 {
		b.key = key
	} else {
		b.key, b.list = key, []row{} // keyless: found by every filterable column, no map
	}
	return b
}

func (b *tableBuf) keyed() bool { return len(b.t.PKColumns) > 0 && b.keyErr == nil }

func (b *tableBuf) keyOf(r row) row {
	k := make(row, len(b.key))
	for i, c := range b.key {
		k[i] = r[c]
	}
	return k
}

func keyString(k row) string { return string(wire.EncodeTSV(nil, k)) }

func (b *tableBuf) insert(r row) {
	if !b.keyed() {
		b.list = append(b.list, r)
		return
	}
	k := keyString(b.keyOf(r))
	if _, ok := b.inserts[k]; !ok {
		b.order = append(b.order, k)
	}
	b.inserts[k] = r
}

// remove deletes a row: an insert of the same period cancels out, the key goes to the
// deletes in any case (the row may be in the target from before).
func (b *tableBuf) remove(r row) {
	if !b.keyed() {
		k := keyString(b.keyOf(r))
		for i := range b.list {
			if keyString(b.keyOf(b.list[i])) == k {
				b.list = append(b.list[:i], b.list[i+1:]...)
				return
			}
		}
		b.dels = append(b.dels, b.keyOf(r))
		return
	}
	kv := b.keyOf(r)
	k := keyString(kv)
	delete(b.inserts, k)
	if _, ok := b.deletes[k]; !ok {
		b.delOrd = append(b.delOrd, k)
	}
	b.deletes[k] = kv
}

func (b *tableBuf) empty() bool {
	return len(b.inserts) == 0 && len(b.deletes) == 0 && len(b.list) == 0 && len(b.dels) == 0 && len(b.loads) == 0
}

// files renders the table's two files: the keys to delete (those of the loaded rows
// included) and the rows to load, the restore's rows last.
func (b *tableBuf) files() (dels, ins []byte, ndel, nins int64) {
	dels, ins, ndel, nins = b.changes()
	for _, r := range b.loads {
		ins = wire.EncodeTSV(ins, r)
		nins++
	}
	return
}

func (b *tableBuf) changes() (dels, ins []byte, ndel, nins int64) {
	if !b.keyed() {
		for _, k := range b.dels {
			dels = wire.EncodeTSV(dels, k)
			ndel++
		}
		for _, r := range b.list {
			ins = wire.EncodeTSV(ins, r)
			nins++
		}
		return
	}
	seen := map[string]bool{}
	for _, k := range b.delOrd {
		seen[k] = true
		dels = wire.EncodeTSV(dels, b.deletes[k])
		ndel++
	}
	// a key is in order once per insert after a delete (an update: delete then insert), its
	// row is written once, the last (preprod: 6 updates of one row loaded it 6 times)
	written := map[string]bool{}
	for _, k := range b.order {
		r, ok := b.inserts[k]
		if !ok || written[k] {
			continue
		}
		written[k] = true
		if !seen[k] {
			seen[k] = true
			dels = wire.EncodeTSV(dels, b.keyOf(r))
			ndel++
		}
		ins = wire.EncodeTSV(ins, r)
		nins++
	}
	return
}

func (b *tableBuf) keyNames() []string {
	names := make([]string, len(b.key))
	for i, c := range b.key {
		names[i] = b.t.Columns[c].Name
	}
	return names
}

// checkpointWriter writes the checkpoints into dir, atomically (a temporary file renamed),
// waiting first while the checkpoints not yet applied exceed maxPending.
type checkpointWriter struct {
	dir        string
	maxPending int64
	paused     func(pending int64) // told while it waits
}

func (w *checkpointWriter) waitRoom() {
	for {
		p := wire.PendingBytes(w.dir)
		if p <= w.maxPending {
			return
		}
		if w.paused != nil {
			w.paused(p)
		}
		time.Sleep(time.Second)
	}
}

// write packs the files and the manifest into checkpoint m.Seq.
func (w *checkpointWriter) write(m wire.Manifest, files map[string][]byte) error {
	w.waitRoom()
	m.Wire = wire.WireVersion
	var buf bytes.Buffer
	// parallel gzip, 1 MB blocks over the CPUs: the gzip of a big period (the restore's
	// rows) was the slow part of a checkpoint
	gz := gzip.NewWriter(&buf)
	if err := gz.SetConcurrency(1<<20, runtime.NumCPU()); err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	for _, n := range names {
		if err := add(n, files[n]); err != nil {
			return err
		}
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := add(wire.ManifestName, mb); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(w.dir, 0750); err != nil {
		return err
	}
	final := filepath.Join(w.dir, wire.CheckpointName(m.Seq))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0640); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// tables renders the buffers of a period into a manifest's tables and their files.
func renderTables(bufs map[string]*tableBuf) ([]wire.TableFiles, map[string][]byte) {
	keys := make([]string, 0, len(bufs))
	for k := range bufs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tables []wire.TableFiles
	files := map[string][]byte{}
	for i, k := range keys {
		b := bufs[k]
		if b.empty() {
			continue
		}
		dels, ins, ndel, nins := b.files()
		tf := wire.TableFiles{Schema: b.t.Schema, Table: b.t.Name, KeyColumns: b.keyNames(), DeleteRows: ndel, InsertRows: nins}
		if ndel > 0 {
			tf.Deletes = fmt.Sprintf("%d.deletes.tsv", i)
			files[tf.Deletes] = dels
		}
		if nins > 0 {
			tf.Inserts = fmt.Sprintf("%d.inserts.tsv", i)
			files[tf.Inserts] = ins
		}
		tables = append(tables, tf)
	}
	return tables, files
}

// tsvField converts a binlog row value to LOAD DATA's text: ENUM and SET as their text
// (VARCHAR on ColumnStore), numbers in decimal.
func tsvField(c *schema.TableColumn, v interface{}) interface{} {
	if v == nil {
		return nil
	}
	v = value(c, v)
	switch n := v.(type) {
	case string, []byte:
		return n
	case int64:
		return strconv.FormatInt(n, 10)
	case int32:
		return strconv.FormatInt(int64(n), 10)
	case int16:
		return strconv.FormatInt(int64(n), 10)
	case int8:
		return strconv.FormatInt(int64(n), 10)
	case int:
		return strconv.Itoa(n)
	case uint64:
		return strconv.FormatUint(n, 10)
	case uint32:
		return strconv.FormatUint(uint64(n), 10)
	case uint16:
		return strconv.FormatUint(uint64(n), 10)
	case uint8:
		return strconv.FormatUint(uint64(n), 10)
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(n), 'g', -1, 32)
	case bool:
		if n {
			return "1"
		}
		return "0"
	}
	return fmt.Sprint(v)
}

// dumpField converts a splitdump value to LOAD DATA's text: a BIT (b'...' or 0x...) is a
// BIGINT UNSIGNED on ColumnStore, any other 0x... value its bytes.
func dumpField(c *schema.TableColumn, v dumpValue) interface{} {
	switch {
	case v.Null:
		return nil
	case v.Bits || (v.Hex && c.Type == schema.TYPE_BIT):
		return bitsToDecimal(v)
	}
	return v.Text
}

// sizeBytes: batch_mb or restore_batch_mb in bytes, 64 MB when unset
func sizeBytes(mb int) int64 {
	if mb <= 0 {
		mb = 64
	}
	return mbBytes(mb)
}

// mbBytes: max_pending_mb in bytes, 1024 MB when unset
func mbBytes(mb int) int64 {
	if mb <= 0 {
		mb = 1024
	}
	if int64(mb) > math.MaxInt64>>20 {
		return math.MaxInt64
	}
	return int64(mb) << 20
}
