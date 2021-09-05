// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

func (configurator *Configurator) DropProxyTag(dtag string) {
	var newtags []string
	for _, tag := range configurator.ProxyTags {
		//	cluster.LogPrintf(LvlInfo, "%s %s", tag, dtag)
		if dtag != tag {
			newtags = append(newtags, tag)
		}
	}
	configurator.ProxyTags = newtags
}

func (configurator *Configurator) DropDBTagConfig(dtag string) bool {

	var newtags []string
	changed := false
	for _, tag := range configurator.DBTags {
		if dtag != tag {
			newtags = append(newtags, tag)
		}
	}
	if len(configurator.DBTags) != len(newtags) {
		changed = true
		configurator.SetDBTags(newtags)
	}
	return changed
}
