// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2268: a settings row of the dashboard names the key its control changes (globalSetting /
// clusterSetting) so the lock shows when the system config sets it. A misspelt key fails
// silently: the lock never shows. Every key named in the dashboard must be a configuration key
// (a mapstructure tag of config.Config, T16).
func TestGUISettingKeysAreFlags(t *testing.T) {
	keys := map[string]bool{}
	ct := reflect.TypeOf(config.Config{})
	for i := 0; i < ct.NumField(); i++ {
		if k := ct.Field(i).Tag.Get("mapstructure"); k != "" {
			keys[k] = true
		}
	}

	keyRe := regexp.MustCompile(`(?:globalSetting|clusterSetting): ['"]([a-z0-9-]+)['"]`)
	unknown := map[string][]string{}
	n := 0
	root := filepath.Join("..", "share", "dashboard_react", "src")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsx") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range keyRe.FindAllStringSubmatch(string(b), -1) {
			n++
			if !keys[m[1]] {
				unknown[m[1]] = append(unknown[m[1]], strings.TrimPrefix(path, root+string(filepath.Separator)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no setting key found in the dashboard: the pattern or the path is wrong")
	}
	if len(unknown) > 0 {
		list := make([]string, 0, len(unknown))
		for k := range unknown {
			list = append(list, k+" ("+strings.Join(unknown[k], ", ")+")")
		}
		sort.Strings(list)
		t.Fatalf("%d setting keys of the dashboard are no configuration key:\n%s", len(unknown), strings.Join(list, "\n"))
	}
}
