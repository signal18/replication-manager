// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/licensebuilder"
	"github.com/spf13/cobra"
)

// license-builder (#2309): the Cloud18 licence-builder, run beside a monitor on the dev image.
// Its setup is the environment of that internal service, see
// doc/implementation/licensebuilder/LICENSE_BUILDER.md.
var licenseBuilderCmd = &cobra.Command{
	Use:   "license-builder",
	Short: "Cloud18 licence-builder: build, test and keep the licence package of a seat",
	RunE: func(cmd *cobra.Command, args []string) error {
		env := func(key, def string) string {
			if v := os.Getenv(key); v != "" {
				return v
			}
			return def
		}
		cfg := licensebuilder.Config{
			RepoURL:         env("LICENSE_BUILDER_REPO_URL", "https://github.com/signal18/replication-manager.git"),
			SourceDir:       env("LICENSE_BUILDER_SOURCE_DIR", "/go/src/github.com/signal18/replication-manager"),
			WorkDir:         env("LICENSE_BUILDER_WORK_DIR", "/var/lib/license-builder/work"),
			OutDir:          env("LICENSE_BUILDER_OUT_DIR", "/var/lib/license-builder/packages"),
			DataDir:         env("LICENSE_BUILDER_DATADIR", "/var/lib/replication-manager"),
			MonitorURL:      env("LICENSE_BUILDER_MONITOR_URL", "https://127.0.0.1:10005"),
			MonitorUser:     env("LICENSE_BUILDER_MONITOR_USER", "admin"),
			MonitorPassword: os.Getenv("LICENSE_BUILDER_MONITOR_PASSWORD"),
			SignerUser:      os.Getenv("PLUGIN_SIGNER_USER"),
			SignerToken:     os.Getenv("PLUGIN_SIGNER_TOKEN"),
			IssuerURL:       env("LICENSE_BUILDER_ISSUER_URL", config.ConstCloud18IdentityURL),
			AllowedClients:  strings.Split(env("LICENSE_BUILDER_ALLOWED_CLIENTS", "cloud18-crm"), ","),
			Addr:            env("LICENSE_BUILDER_ADDR", ":10010"),
		}
		if cfg.MonitorPassword == "" || cfg.SignerUser == "" || cfg.SignerToken == "" {
			return fmt.Errorf("LICENSE_BUILDER_MONITOR_PASSWORD, PLUGIN_SIGNER_USER and PLUGIN_SIGNER_TOKEN are required")
		}
		b := licensebuilder.New(cfg, log.Printf)
		go b.Run()
		log.Printf("license-builder: listening on %s, monitor %s", cfg.Addr, cfg.MonitorURL)
		srv := &http.Server{Addr: cfg.Addr, Handler: b.Handler(), ReadHeaderTimeout: 10 * time.Second}
		return srv.ListenAndServe()
	},
}

func init() {
	rootCmd.AddCommand(licenseBuilderCmd)
}
