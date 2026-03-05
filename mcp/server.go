// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"fmt"
	"net"
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

// RepmanProvider is the interface that the MCP server uses to access replication-manager.
// It is implemented by *server.ReplicationManager. Defined here to avoid import cycles.
type RepmanProvider interface {
	GetClusters() map[string]*cluster.Cluster
	GetClusterByName(name string) *cluster.Cluster
	GetVersion() string
	GetFullVersion() string
	GetStatus() string
	GetConf() *config.Config
	// SetClusterSetting sets a named configuration key to a value for a cluster.
	SetClusterSetting(cl *cluster.Cluster, key, value string) error
	// SwitchClusterSetting toggles a boolean configuration key for a cluster.
	SwitchClusterSetting(cl *cluster.Cluster, key string) error
}

// MCPServer encapsulates the MCP server and its configuration.
type MCPServer struct {
	repman     RepmanProvider
	conf       *config.Config
	mcp        *mcpserver.MCPServer
	sseServer  *mcpserver.SSEServer
	httpServer *http.Server
	cancelFunc context.CancelFunc
	logger     *log.Logger
}

// NewMCPServer creates and configures a new MCPServer instance.
func NewMCPServer(repman RepmanProvider, conf *config.Config, logger *log.Logger) *MCPServer {
	s := &MCPServer{
		repman: repman,
		conf:   conf,
		logger: logger,
	}

	s.mcp = mcpserver.NewMCPServer(
		"replication-manager",
		conf.Version,
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, true),
		mcpserver.WithPromptCapabilities(true),
		mcpserver.WithInstructions(serverInstructions),
	)

	s.registerResources()
	s.registerReadOnlyTools()
	if conf.MCPWriteEnabled {
		s.registerWriteTools()
	}
	s.registerPrompts()

	return s
}

// Start starts the MCP server based on the configured transport.
func (s *MCPServer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancelFunc = cancel

	transport := s.conf.MCPTransport
	// addr is what the TCP listener binds to (may be 0.0.0.0).
	// baseURL is advertised to clients in the SSE handshake; it must match the
	// address clients actually use to reach the server.
	// Priority: mcp-advertise-address > mcp-bind-address (normalized) > localhost.
	addr := fmt.Sprintf("%s:%s", s.conf.MCPBindAddr, s.conf.MCPPort)
	baseURL := s.conf.MCPAdvertiseAddr
	if baseURL == "" {
		baseHost := s.conf.MCPBindAddr
		if baseHost == "" || baseHost == "0.0.0.0" {
			baseHost = "localhost"
		}
		baseURL = fmt.Sprintf("http://%s:%s", baseHost, s.conf.MCPPort)
	}

	writeMode := "read-only"
	if s.conf.MCPWriteEnabled {
		writeMode = "read-write"
	}

	switch transport {
	case "stdio":
		s.logger.Infof("MCP server started: transport=stdio version=%s mode=%s", s.conf.Version, writeMode)
		return mcpserver.ServeStdio(s.mcp)
	case "sse", "":
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("MCP server failed to bind %s: %w", addr, err)
		}
		s.sseServer = mcpserver.NewSSEServer(s.mcp,
			mcpserver.WithBaseURL(baseURL),
		)
		s.httpServer = &http.Server{Addr: addr, Handler: s.sseServer}
		s.logger.Infof("MCP server started: transport=sse listen=%s version=%s mode=%s", ln.Addr(), s.conf.Version, writeMode)
		s.logger.Infof("MCP SSE endpoint: %s/sse  (use this URL with 'claude mcp add')", baseURL)
		return s.httpServer.Serve(ln)
	case "both":
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("MCP server failed to bind %s: %w", addr, err)
		}
		s.sseServer = mcpserver.NewSSEServer(s.mcp,
			mcpserver.WithBaseURL(baseURL),
		)
		s.httpServer = &http.Server{Addr: addr, Handler: s.sseServer}
		s.logger.Infof("MCP server started: transport=both listen=%s version=%s mode=%s", ln.Addr(), s.conf.Version, writeMode)
		s.logger.Infof("MCP SSE endpoint: %s/sse  (use this URL with 'claude mcp add')", baseURL)
		go func() {
			if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
				s.logger.Errorf("MCP SSE server error: %v", err)
			}
		}()
		// Serve stdio in foreground
		return mcpserver.ServeStdio(s.mcp)
	default:
		return fmt.Errorf("unknown MCP transport: %s", transport)
	}
}

// Stop shuts down the MCP server.
func (s *MCPServer) Stop() {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	if s.httpServer != nil {
		s.httpServer.Shutdown(context.Background())
	}
}
