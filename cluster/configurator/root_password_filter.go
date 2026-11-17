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
// sstAuth says what becomes of a wsrep_sst_auth line carrying the password (SSTAuthFor):
// SSTAuthSocket rewrites it to the unix_socket account, SSTAuthKeep leaves it (an SST that
// cannot authenticate otherwise), SSTAuthDrop comments it out like any other line (not a
// Galera cluster: no SST ever reads it).
func FilterRootPassword(fpath string, content string, password string, sstAuth SSTAuth) (string, []string) {
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
		if key == "wsrep_sst_auth" && sstAuth == SSTAuthKeep {
			continue
		}
		if key == "wsrep_sst_auth" && sstAuth == SSTAuthSocket {
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

// SSTAuth is what the root password filter does with a wsrep_sst_auth line.
type SSTAuth int

const (
	// SSTAuthDrop: not a Galera cluster, the line is commented out (no SST reads it)
	SSTAuthDrop SSTAuth = iota
	// SSTAuthSocket: the SST authenticates the unix_socket account, the line names it
	SSTAuthSocket
	// SSTAuthKeep: the SST cannot authenticate otherwise, the root password stays (a state
	// says why: SSTAuthKeepReason)
	SSTAuthKeep
)

// SSTAuthKeepReason is why a Galera cluster's SST needs the root password in its
// configuration, "" when it does not (OpenSVC and Kubernetes, where replication-manager keeps
// the unix_socket account, CheckGaleraSSTAccount):
//   - the mysqldump method: the donor logs in to the joiner over the network, a socket
//     account cannot;
//   - MySQL and Percona: their xtrabackup SST over auth_socket is not validated.
//
// MariaDB 10.3 and older are not a reason: unsupported on Galera (ERR00115).
func SSTAuthKeepReason(galera bool, method string, mariadb bool) string {
	switch {
	case !galera:
		return ""
	case strings.EqualFold(strings.TrimSpace(method), "mysqldump"):
		return "the SST method mysqldump logs in to the joiner over the network"
	case !mariadb:
		return "the SST of MySQL and Percona over auth_socket is not validated"
	}
	return ""
}

// SSTAuthFor is the filter's handling of wsrep_sst_auth for this configuration.
func (configurator *Configurator) SSTAuthFor(method string) SSTAuth {
	galera := configurator.IsFilterInDBTags("wsrep")
	switch {
	case !galera:
		return SSTAuthDrop
	case SSTAuthKeepReason(galera, method, configurator.mariaDBImage()) != "":
		return SSTAuthKeep
	}
	return SSTAuthSocket
}

// mariaDBImage: the database image is MariaDB (no image: the MariaDB default).
func (configurator *Configurator) mariaDBImage() bool {
	img := configurator.ClusterConfig.ProvDbImg
	return img == "" || releases.FlavorOfImage(img) == "mariadb"
}

// galeraSocketSST: a Galera cluster on MariaDB authenticates its SST with the unix_socket
// account. MySQL and Percona keep their wsrep_sst_auth until xtrabackup SST over
// auth_socket is validated.
func (configurator *Configurator) galeraSocketSST() bool {
	return configurator.IsFilterInDBTags("wsrep") && configurator.mariaDBImage()
}

// SSTAuthKeepReasonFor is SSTAuthKeepReason for this configuration: why the rendered
// wsrep_sst_auth keeps the root password, "" when it does not.
func (configurator *Configurator) SSTAuthKeepReasonFor(method string) string {
	return SSTAuthKeepReason(configurator.IsFilterInDBTags("wsrep"), method, configurator.mariaDBImage())
}

// shortRootPasswordLen: below it, a root password can equal an unrelated value by chance
// (wsrep_cluster_name=mysql): the filter warns with what it commented out.
const shortRootPasswordLen = 12
