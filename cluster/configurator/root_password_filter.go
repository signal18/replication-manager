// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// rootPasswordFileSuffix is the one rendered file whose job is to carry the root
// password: the container init secret, sourced by the launcher and the dbjobs.
const rootPasswordFileSuffix = "/init/MYSQL_ROOT_PASSWORD"

// FilterRootPassword removes the root password from a rendered database
// configuration file (#1960). It works on the rendered content and the real
// password, never on the fragment's name or placeholder: the moduleset comes from
// the compliance collector and cannot be trusted to keep the password out.
//
//   - A wsrep_sst_auth line becomes "wsrep_sst_auth=<socket user>:", the Galera SST
//     authenticating the unix_socket account the monitor maintains (no password exists).
//   - Any other line carrying the password is replaced by a comment naming its key:
//     the backups and the dbjobs pass their credentials on the command line.
//
// It returns the content and the keys it changed. The init secret file is left as is.
//
// socketSST: the SST authenticates the unix_socket account (MariaDB). Without it
// (MySQL/Percona, xtrabackup SST not validated with auth_socket) the wsrep_sst_auth line
// is left as it is.
func FilterRootPassword(fpath string, content string, password string, socketSST bool) (string, []string) {
	if password == "" || strings.HasSuffix(fpath, rootPasswordFileSuffix) || !strings.Contains(content, password) {
		return content, nil
	}
	var changed []string
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !lineCarriesPassword(line, password) {
			continue
		}
		key := iniLineKey(line)
		if key == "wsrep_sst_auth" && !socketSST {
			continue
		}
		if key == "wsrep_sst_auth" {
			lines[i] = "wsrep_sst_auth=" + config.ConstGaleraSSTSocketUser + ":"
		} else {
			if key == "" {
				key = "line"
			}
			lines[i] = "# " + key + ": removed by replication-manager, the root password is never written in the database configuration"
		}
		changed = append(changed, key)
	}
	return strings.Join(lines, "\n"), changed
}

// iniLineKey returns the option name of an ini line, normalised (lower case,
// dashes as underscores), or "" when the line has no "key = value" shape.
func iniLineKey(line string) string {
	k, _, found := strings.Cut(strings.TrimSpace(line), "=")
	if !found {
		return ""
	}
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" || strings.ContainsAny(k, " \t\"'$") {
		return ""
	}
	return strings.ReplaceAll(k, "-", "_")
}

// lineCarriesPassword tells whether a rendered line holds the password as a value, not
// merely the same letters somewhere: a short or common password ("root", "mysql") must not
// comment out user=root or datadir=/var/lib/mysql (review of #1962). Comments and section
// headers never carry it; on a key = value line only the value side counts, and a key
// naming a user is a user name, not a secret. A token matches when it IS the password, or
// the client's -p<password> form.
func lineCarriesPassword(line, password string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "[") || !strings.Contains(t, password) {
		return false
	}
	value := t
	if key := iniLineKey(t); key != "" {
		if key == "user" || strings.HasSuffix(key, "_user") {
			return false
		}
		_, value, _ = strings.Cut(t, "=")
	}
	// a password holding a separator is never a whole token: look for it in the value
	if strings.ContainsAny(password, passwordTokenSeparators) {
		return strings.Contains(value, password)
	}
	for _, tok := range strings.FieldsFunc(value, func(r rune) bool { return strings.ContainsRune(passwordTokenSeparators, r) }) {
		if tok == password || tok == "-p"+password {
			return true
		}
	}
	return false
}

// passwordTokenSeparators split a value into the tokens a password is compared with.
const passwordTokenSeparators = ":,; \t\"'="

// galeraSSTAccountInitFile is the datadir-init SQL creating the SST account (init/ is the
// image's /docker-entrypoint-initdb.d, which runs *.sql once, at datadir creation).
const galeraSSTAccountInitFile = "galera_sst_account.sql"

// galeraSocketSST: a Galera cluster on MariaDB authenticates its SST with the unix_socket
// account. MySQL and Percona keep their wsrep_sst_auth until xtrabackup SST over
// auth_socket is validated.
func (configurator *Configurator) galeraSocketSST() bool {
	if !configurator.IsFilterInDBTags("wsrep") {
		return false
	}
	img := configurator.ClusterConfig.ProvDbImg
	return img == "" || releases.FlavorOfImage(img) == "mariadb"
}

// shortRootPasswordLen: below it, a root password can equal an unrelated value by chance
// (wsrep_cluster_name=mysql): the filter warns with what it commented out.
const shortRootPasswordLen = 12
