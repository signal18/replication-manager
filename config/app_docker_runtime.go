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
