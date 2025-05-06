//go:build clients
// +build clients

package share

import "embed"

// EmbededDbModuleFS is an empty FS when built with 'clients' tag
var EmbededDbModuleFS embed.FS
