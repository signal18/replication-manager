// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The lock of the alert channels (doc/implementation/cluster/ALERT_CHANNELS.md): an alert is
// sent by the channels of the registry only, through cluster.dispatchAlert. A send line is
// a call that fires a Slack or Pushover hook (a logging method on LogSlack or LogPushover),
// a call to the senders the channels wrap (EmailMessage, sendMsTeams), a call to the old
// direct senders (BashScriptAlert, PostMeetMessage), a run of alert-script, or a use of one
// of the new backends' clients (alertmanager.New, telegram.New).
//
// Unlike the orchestrator lock, nothing is allowed outside the channel files: the migration
// is done in one step. The configuration calls (LogSlack.Activate, SetURL, IsHookActive...)
// are not sends.
var alertSendAllowedPrefix = "cluster/alert_channel_"

var alertHookSendMethods = map[string]bool{
	"WithFields": true, "WithField": true, "WithTime": true, "WithError": true,
	"Log": true, "Logf": true, "Print": true, "Printf": true, "Println": true,
	"Error": true, "Errorf": true, "Warn": true, "Warnf": true, "Warning": true, "Warningf": true,
	"Info": true, "Infof": true, "Debug": true, "Debugf": true, "Fatal": true, "Panic": true,
}

var alertSenderFuncs = map[string]bool{
	"EmailMessage": true, "sendMsTeams": true, "BashScriptAlert": true, "PostMeetMessage": true,
}

// alertSendLines returns the send lines of one Go file.
func alertSendLines(t *testing.T, path string) map[int]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	lines := map[int]string{}
	mark := func(n ast.Node, what string) { lines[fset.Position(n.Pos()).Line] = what }
	isField := func(e ast.Expr, names ...string) bool {
		var name string
		switch x := e.(type) {
		case *ast.SelectorExpr:
			name = x.Sel.Name
		case *ast.Ident:
			name = x.Name
		default:
			return false
		}
		for _, n := range names {
			if name == n {
				return true
			}
		}
		return false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if alertHookSendMethods[fun.Sel.Name] && isField(fun.X, "LogSlack", "LogPushover") {
				mark(call, "a Slack or Pushover hook fired directly")
			}
			if alertSenderFuncs[fun.Sel.Name] {
				mark(call, fun.Sel.Name)
			}
			if pkg, ok := fun.X.(*ast.Ident); ok && fun.Sel.Name == "New" && (pkg.Name == "alertmanager" || pkg.Name == "telegram") {
				mark(call, pkg.Name+".New")
			}
			if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "exec" && strings.HasPrefix(fun.Sel.Name, "Command") {
				for _, a := range call.Args {
					if isField(a, "AlertScript") {
						mark(call, "alert-script run directly")
					}
				}
			}
		case *ast.Ident:
			if alertSenderFuncs[fun.Name] {
				mark(call, fun.Name)
			}
		}
		return true
	})
	return lines
}

func TestNoAlertSendOutsideRegistry(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	seen := 0
	for _, dir := range []string{"cluster", "server", "mcp", "regtest", "utils/alert"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := dir + "/" + name
			lines := alertSendLines(t, filepath.Join(root, dir, name))
			if strings.HasPrefix(rel, alertSendAllowedPrefix) {
				seen += len(lines)
				continue
			}
			for line, what := range lines {
				problems = append(problems, fmt.Sprintf("%s:%d: %s: send the alert through cluster.dispatchAlert (an AlertChannel of alert_channel_registry.go)", rel, line, what))
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if seen == 0 {
		t.Fatal("no send line found in the channel files: the pattern is wrong")
	}
}
