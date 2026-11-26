// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// runCreate is a plugin run with -create, at the seed of a child: each table of the rules on
// the target (restored from the parent's backup, as the source describes it) refused by the
// destination with its reason, or made the destination's; one wire.CreateResult JSON line
// per table on stdout. 0 when every table was examined, 1 on an error that stopped it.
func runCreate(cfg wire.Config, dest Destination) int {
	db, err := TargetDB(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer conn.Close()
	for _, q := range dest.Session() {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			fmt.Fprintln(os.Stderr, q+": "+err.Error())
			return 1
		}
	}
	out := json.NewEncoder(os.Stdout)
	// each schema listed once, each table examined once (two rules on one schema)
	schemas, seen := map[string]bool{}, map[string]bool{}
	for _, r := range cfg.Rules {
		if schemas[strings.ToLower(r.Schema)] {
			continue
		}
		schemas[strings.ToLower(r.Schema)] = true
		tables, q, err := dbhelper.GetTablesByEngine(db, nil, "", r.Schema, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", q, err)
			return 1
		}
		for _, t := range tables {
			if !cfg.Matches(t.Schema, t.Table) || seen[t.Schema+"."+t.Table] {
				continue
			}
			seen[t.Schema+"."+t.Table] = true
			res := wire.CreateResult{Table: t.Schema + "." + t.Table, Engine: t.Engine}
			d, err := ReadTableDesc(db, t.Schema, t.Table)
			if err != nil {
				res.Reason = "its description could not be read: " + err.Error()
			} else if res.Reason = dest.Refuse(d); res.Reason == "" {
				if err := dest.Create(ctx, conn, d); err != nil {
					res.Reason = "the conversion failed: " + strings.TrimSpace(err.Error())
				} else {
					res.Created = true
				}
			}
			out.Encode(res)
		}
	}
	return 0
}
