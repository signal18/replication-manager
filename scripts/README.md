# Installation Scripts

This directory contains installation and utility scripts for replication-manager.

## Available Scripts

### install.sh

One-liner installation script for the replication-manager embedded binary.

**Usage:**

```bash
# Install latest version (auto-detect OS and architecture)
curl -fsSL https://signal18.io/get-repman | bash

# Install with sudo (system-wide installation)
curl -fsSL https://signal18.io/get-repman | sudo bash

# Install specific version
curl -fsSL https://signal18.io/get-repman | REPMAN_VERSION=v3.1.16 bash

# Install server + CLI client
curl -fsSL https://signal18.io/get-repman | REPMAN_INSTALL_CLI=true bash

# Custom installation directory
curl -fsSL https://signal18.io/get-repman | REPMAN_INSTALL_DIR=/opt/repman bash
```

**Environment Variables:**

| Variable | Description | Default |
|----------|-------------|---------|
| `REPMAN_VERSION` | Specific version to install | latest |
| `REPMAN_INSTALL_DIR` | Custom installation directory | `/usr/local/bin` or `~/.local/bin` |
| `REPMAN_INSTALL_CLI` | Install CLI client alongside server | `false` |
| `REPMAN_SKIP_VERIFY` | Skip post-installation verification | `false` |

**Supported Platforms:**

- Linux: amd64, arm64
- macOS (Darwin): amd64 (Intel), arm64 (Apple Silicon)

**Features:**

- Auto-detect operating system and architecture
- Download latest release from GitHub
- Auto-fallback to user directory if sudo not available
- Optional CLI client installation
- Post-installation verification
- Comprehensive error handling

### HOSTING.md

Documentation for hosting the `install.sh` script on signal18.io. Contains:

- Web server configuration examples (nginx, Apache)
- SSL certificate setup instructions
- Deployment steps
- Testing procedures
- Security considerations
- Maintenance guidelines

See [HOSTING.md](HOSTING.md) for complete hosting documentation.

## Installation Flow

1. Detect platform (OS + architecture)
2. Fetch latest release information from GitHub API
3. Download appropriate binary tarball
4. Extract binary to temporary directory
5. Install to target directory (`/usr/local/bin` or `~/.local/bin`)
6. Verify installation
7. Display success message and next steps
8. Clean up temporary files

## Development

### Testing Locally

```bash
# Test with specific version
REPMAN_VERSION=v3.1.16 REPMAN_INSTALL_DIR=/tmp/test-install ./scripts/install.sh

# Verify installation
/tmp/test-install/replication-manager version

# Cleanup
rm -rf /tmp/test-install
```

### Checking Syntax

```bash
bash -n scripts/install.sh
```

## Contributing

When modifying `install.sh`:

1. Ensure all output goes to stderr (`>&2`) to avoid polluting stdout during command substitution
2. Use the `info()`, `success()`, `warning()`, and `error_exit()` functions for user feedback
3. Test on multiple platforms (Linux amd64/arm64, macOS amd64/arm64)
4. Update documentation as needed
5. Test both with and without sudo
6. Test with environment variable overrides

## Support

For issues or questions:
- GitHub Issues: https://github.com/signal18/replication-manager/issues
- Repository: https://github.com/signal18/replication-manager
