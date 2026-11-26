// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package config

import (
	"fmt"
	"regexp"
	"strings"
)

// prov-app-docker-runtime: the docker runtime of the containers that run a client app image
// (the app container, its jobs sidecar, its reload task). The client chooses the image and
// the command freely; the isolation comes from the runtime, which only the instance admin
// sets (scope server). See doc/implementation/cluster/APP_SANDBOX_RUNTIME.md.
const (
	// ConstAppDockerRuntimeAuto: the sandbox on a Cloud18 instance, the node's default
	// runtime otherwise. The default value of the setting.
	ConstAppDockerRuntimeAuto = "auto"
	// ConstAppDockerRuntimeNone: the node's default runtime (the off-switch, like an empty value).
	ConstAppDockerRuntimeNone = "none"
	// ConstAppDockerRuntimeSandbox: gVisor registered with the host network
	// (runtimeArgs ["--network=host"]). Plain runsc runs its own network stack, which takes
	// the address of the namespace the app joins (the pause container's) and breaks the
	// pause and every sidecar of the service.
	ConstAppDockerRuntimeSandbox = "runsc-hostnet"
)

// appDockerRuntimeRe: the name of a runtime registered in the docker daemon. The value is
// written into the run_args of an orchestrator configuration: no space, quote or option.
var appDockerRuntimeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateAppDockerRuntime validates a prov-app-docker-runtime value: empty, auto, none or
// a runtime name.
func ValidateAppDockerRuntime(value string) error {
	v := strings.TrimSpace(value)
	if v == "" || v == ConstAppDockerRuntimeAuto || v == ConstAppDockerRuntimeNone {
		return nil
	}
	if !appDockerRuntimeRe.MatchString(v) {
		return fmt.Errorf("prov-app-docker-runtime must be auto, none, empty or a docker runtime name such as %s, got %q", ConstAppDockerRuntimeSandbox, value)
	}
	return nil
}

// GetAppDockerRuntime returns the docker runtime the client app containers run under, ""
// for the node's default runtime: auto resolves to the sandbox on a Cloud18 instance only,
// none and empty are off. An invalid value (one written in a configuration file: the API
// refuses it) is an error, never a silent fallback to the default runtime: the provisioning
// that renders it stops.
func (conf *Config) GetAppDockerRuntime() (string, error) {
	v := strings.TrimSpace(conf.ProvAppDockerRuntime)
	switch v {
	case ConstAppDockerRuntimeAuto:
		if conf.Cloud18 {
			return ConstAppDockerRuntimeSandbox, nil
		}
		return "", nil
	case "", ConstAppDockerRuntimeNone:
		return "", nil
	}
	if err := ValidateAppDockerRuntime(v); err != nil {
		return "", err
	}
	return v, nil
}

// prov-app-docker-trusted: the apps the instance admin trusts (an app of the instance's own,
// its own code, not exposed to the clients) run under the node's default runtime, never
// prov-app-docker-runtime. A list of app ids, not of names: the value is a server key that
// every cluster's configuration carries, so it names no cluster and no app of another client.
// See doc/implementation/cluster/APP_SANDBOX_RUNTIME.md.

// appIDRe: an app id as App.SetID writes it, "ap" and a crc64 in decimal.
var appIDRe = regexp.MustCompile(`^ap[0-9]{1,20}$`)

// AppDockerTrustedIDs returns the app ids of a prov-app-docker-trusted value, trimmed, empty
// entries dropped, each once, in their order.
func AppDockerTrustedIDs(value string) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range strings.Split(value, ",") {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// ValidateAppDockerTrusted validates a prov-app-docker-trusted value: empty, or a comma list
// of app ids (ap<n>).
func ValidateAppDockerTrusted(value string) error {
	for _, id := range AppDockerTrustedIDs(value) {
		if !appIDRe.MatchString(id) {
			return fmt.Errorf("prov-app-docker-trusted must be a comma list of app ids such as ap1234567890, got %q", id)
		}
	}
	return nil
}

// IsAppDockerTrusted reports whether the instance admin trusts the app of this id
// (prov-app-docker-trusted): its containers then run under the node's default runtime.
func (conf *Config) IsAppDockerTrusted(appID string) bool {
	if appID == "" {
		return false
	}
	for _, id := range AppDockerTrustedIDs(conf.ProvAppDockerTrusted) {
		if id == appID {
			return true
		}
	}
	return false
}
