// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

func (configurator *Configurator) AddProxyTag(tag string) {
	configurator.ProxyTags = append(configurator.ProxyTags, tag)
}
func (configurator *Configurator) AddDBTag(tag string) {
	configurator.DBTags = append(configurator.DBTags, tag)
}
