#!/usr/bin/env bash
#
# Replication Manager Installation Script
# https://github.com/signal18/replication-manager
#
# This script downloads and installs the latest replication-manager embedded binary
# for your platform (Linux/macOS on amd64/arm64).
#
# Usage:
#   curl -fsSL https://signal18.io/get-repman | bash
#
# Environment variables:
#   REPMAN_VERSION       - Specific version to install (default: latest)
#   REPMAN_INSTALL_DIR   - Custom installation directory
#   REPMAN_INSTALL_CLI   - Set to 'true' to also install CLI client
#   REPMAN_SKIP_VERIFY   - Set to 'true' to skip post-installation verification
#
# Examples:
#   # Install latest version with sudo
#   curl -fsSL https://signal18.io/get-repman | sudo bash
#
#   # Install to user directory (auto-fallback if no sudo)
#   curl -fsSL https://signal18.io/get-repman | bash
#
#   # Install specific version with CLI client
#   curl -fsSL https://signal18.io/get-repman | REPMAN_VERSION=v3.1.15 REPMAN_INSTALL_CLI=true bash
#

set -euo pipefail

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Constants
REPO_OWNER="signal18"
REPO_NAME="replication-manager"
GITHUB_API_URL="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}"
GITHUB_RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download"

# Global variables
DETECTED_OS=""
DETECTED_ARCH=""
VERSION="${REPMAN_VERSION:-}"
INSTALL_DIR="${REPMAN_INSTALL_DIR:-}"
INSTALL_CLI="${REPMAN_INSTALL_CLI:-false}"
SKIP_VERIFY="${REPMAN_SKIP_VERIFY:-false}"
TEMP_DIR=""

#######################################
# Print colored message
# Arguments:
#   $1 - Color code
#   $2 - Message
#######################################
print_msg() {
    local color="$1"
    local message="$2"
    echo -e "${color}${message}${NC}" >&2
}

#######################################
# Print error and exit
# Arguments:
#   $1 - Error message
#######################################
error_exit() {
    print_msg "$RED" "ERROR: $1"
    cleanup
    exit 1
}

#######################################
# Print info message
# Arguments:
#   $1 - Message
#######################################
info() {
    print_msg "$BLUE" "INFO: $1"
}

#######################################
# Print success message
# Arguments:
#   $1 - Message
#######################################
success() {
    print_msg "$GREEN" "SUCCESS: $1"
}

#######################################
# Print warning message
# Arguments:
#   $1 - Message
#######################################
warning() {
    print_msg "$YELLOW" "WARNING: $1"
}

#######################################
# Cleanup temporary files
#######################################
cleanup() {
    if [ -n "$TEMP_DIR" ] && [ -d "$TEMP_DIR" ]; then
        rm -rf "$TEMP_DIR"
    fi
}

# Ensure cleanup on exit
trap cleanup EXIT

#######################################
# Detect operating system
# Returns:
#   "linux", "darwin", or "unsupported"
#######################################
detect_os() {
    local os
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"

    case "$os" in
        linux*)
            echo "linux"
            ;;
        darwin*)
            echo "darwin"
            ;;
        *)
            echo "unsupported"
            ;;
    esac
}

#######################################
# Detect system architecture
# Returns:
#   "amd64", "arm64", or "unsupported"
#######################################
detect_arch() {
    local arch
    arch="$(uname -m)"

    case "$arch" in
        x86_64|amd64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        *)
            echo "unsupported"
            ;;
    esac
}

#######################################
# Check if command exists
# Arguments:
#   $1 - Command name
# Returns:
#   0 if exists, 1 otherwise
#######################################
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

#######################################
# Get latest release version from GitHub that has binaries
# Returns:
#   Version tag (e.g., v3.1.16)
#######################################
get_latest_version() {
    local version
    local releases_json

    info "Fetching latest release information from GitHub..."

    # Fetch recent releases (not just "latest" which may not have binaries)
    if command_exists curl; then
        releases_json=$(curl -fsSL "${GITHUB_API_URL}/releases?per_page=10")
    elif command_exists wget; then
        releases_json=$(wget -qO- "${GITHUB_API_URL}/releases?per_page=10")
    else
        error_exit "Neither curl nor wget is available. Please install one of them."
    fi

    if [ -z "$releases_json" ]; then
        error_exit "Failed to fetch releases from GitHub API"
    fi

    # Try to find a release that has the binary asset we need
    # Extract tag_name from releases, then verify the asset exists
    local tags
    tags=$(echo "$releases_json" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    version=""
    for tag in $tags; do
        # Check if this release has our binary by extracting the release section
        local release_section
        release_section=$(echo "$releases_json" | sed -n "/\"tag_name\": \"$tag\"/,/\"tag_name\":/p")

        if echo "$release_section" | grep -q "\"name\": \"replication-manager-${DETECTED_OS}-${DETECTED_ARCH}.tar.gz\""; then
            version="$tag"
            break
        fi
    done

    # Fallback: just use the first release tag if the above didn't work
    if [ -z "$version" ]; then
        warning "Could not find release with matching binary, using latest release tag"
        version=$(echo "$tags" | head -1)
    fi

    if [ -z "$version" ]; then
        error_exit "Failed to determine latest version from GitHub API"
    fi

    echo "$version"
}

#######################################
# Determine installation directory
# Returns:
#   Installation directory path
#######################################
determine_install_dir() {
    local install_dir="/usr/local/bin"

    # If custom directory specified, use it
    if [ -n "$INSTALL_DIR" ]; then
        echo "$INSTALL_DIR"
        return
    fi

    # Check if we can write to /usr/local/bin
    if [ -w "$install_dir" ] || [ "$(id -u)" -eq 0 ]; then
        echo "$install_dir"
    else
        # Fallback to user local bin
        install_dir="$HOME/.local/bin"
        mkdir -p "$install_dir"
        echo "$install_dir"
    fi
}

#######################################
# Check if directory is in PATH
# Arguments:
#   $1 - Directory path
# Returns:
#   0 if in PATH, 1 otherwise
#######################################
check_path_includes() {
    local dir="$1"
    case ":$PATH:" in
        *":$dir:"*)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

#######################################
# Download binary tarball
# Arguments:
#   $1 - Version
#   $2 - OS
#   $3 - Architecture
#   $4 - Binary type ("server" or "cli")
#   $5 - Output path
#######################################
download_binary() {
    local version="$1"
    local os="$2"
    local arch="$3"
    local binary_type="$4"
    local output_path="$5"
    local binary_name="replication-manager"
    local url

    if [ "$binary_type" = "cli" ]; then
        binary_name="replication-manager-cli"
    fi

    url="${GITHUB_RELEASE_URL}/${version}/${binary_name}-${os}-${arch}.tar.gz"

    info "Downloading ${binary_name}-${os}-${arch}.tar.gz from GitHub..."

    if command_exists curl; then
        if ! curl -fL# -o "$output_path" "$url"; then
            error_exit "Failed to download from $url"
        fi
    elif command_exists wget; then
        if ! wget --show-progress -q -O "$output_path" "$url"; then
            error_exit "Failed to download from $url"
        fi
    else
        error_exit "Neither curl nor wget is available"
    fi
}

#######################################
# Extract binary from tarball
# Arguments:
#   $1 - Tarball path
#   $2 - Output directory
# Returns:
#   Path to extracted binary
#######################################
extract_binary() {
    local tarball="$1"
    local output_dir="$2"

    info "Extracting archive..."

    # Extract the tarball
    if ! tar -xzf "$tarball" -C "$output_dir" 2>/dev/null; then
        error_exit "Failed to extract tarball"
    fi

    # The tarball contains a single binary file, find it
    # Look for any file (excluding .tar.gz files)
    local binary
    for file in "$output_dir"/*; do
        if [ -f "$file" ] && [ "${file##*.}" != "gz" ] && [ "$(basename "$file")" != "*.tar.gz" ]; then
            binary="$file"
            break
        fi
    done

    if [ -z "$binary" ] || [ ! -f "$binary" ]; then
        error_exit "Could not find binary in extracted archive"
    fi

    echo "$binary"
}

#######################################
# Install binary to destination
# Arguments:
#   $1 - Source binary path
#   $2 - Destination directory
#   $3 - Binary name
#######################################
install_binary() {
    local source="$1"
    local dest_dir="$2"
    local binary_name="$3"
    local dest_path="${dest_dir}/${binary_name}"

    info "Installing ${binary_name} to ${dest_dir}..."

    # Create destination directory if it doesn't exist
    mkdir -p "$dest_dir"

    # Copy binary
    if ! cp "$source" "$dest_path"; then
        error_exit "Failed to copy binary to $dest_path"
    fi

    # Make executable
    if ! chmod +x "$dest_path"; then
        error_exit "Failed to make binary executable"
    fi

    success "Installed ${binary_name} to ${dest_path}"
}

#######################################
# Verify installation
# Arguments:
#   $1 - Binary path
#######################################
verify_installation() {
    local binary_path="$1"

    if [ "$SKIP_VERIFY" = "true" ]; then
        return 0
    fi

    info "Verifying installation..."

    if [ ! -x "$binary_path" ]; then
        error_exit "Binary is not executable: $binary_path"
    fi

    # Try to run version command
    if "$binary_path" version >/dev/null 2>&1; then
        success "Installation verified successfully"
        return 0
    else
        warning "Could not verify installation by running 'version' command"
        return 1
    fi
}

#######################################
# Print post-installation instructions
# Arguments:
#   $1 - Installation directory
#   $2 - Installed version
#   $3 - CLI was installed (true/false)
#######################################
print_post_install_info() {
    local install_dir="$1"
    local version="$2"
    local cli_installed="$3"

    echo "" >&2
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" >&2
    success "Replication Manager ${version} installed successfully!"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" >&2
    echo "" >&2

    echo "📍 Installation location:" >&2
    echo "   • Server: ${install_dir}/replication-manager" >&2
    if [ "$cli_installed" = "true" ]; then
        echo "   • CLI:    ${install_dir}/replication-manager-cli" >&2
    fi
    echo "" >&2

    # Check if install_dir is in PATH
    if ! check_path_includes "$install_dir"; then
        warning "Installation directory is not in your PATH"
        echo "" >&2
        echo "To add it to your PATH, run:" >&2
        echo "" >&2
        echo "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.bashrc" >&2
        echo "  source ~/.bashrc" >&2
        echo "" >&2
        echo "Or for zsh:" >&2
        echo "" >&2
        echo "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.zshrc" >&2
        echo "  source ~/.zshrc" >&2
        echo "" >&2
    fi

    echo "🔍 Verify installation:" >&2
    if check_path_includes "$install_dir"; then
        echo "   replication-manager version" >&2
    else
        echo "   ${install_dir}/replication-manager version" >&2
    fi
    echo "" >&2

    echo "📝 Configuration:" >&2
    echo "   You will need to create a configuration file. Suggested locations:" >&2
    echo "   • System-wide: /etc/replication-manager/config.toml" >&2
    echo "   • User-level:  ~/.config/replication-manager/config.toml" >&2
    echo "" >&2

    echo "📚 Documentation:" >&2
    echo "   • GitHub: https://github.com/${REPO_OWNER}/${REPO_NAME}" >&2
    echo "   • README: https://github.com/${REPO_OWNER}/${REPO_NAME}#readme" >&2
    echo "" >&2

    if [ "$cli_installed" != "true" ]; then
        echo "💡 Tip:" >&2
        echo "   To also install the CLI client, run:" >&2
        echo "   curl -fsSL https://signal18.io/get-repman | REPMAN_INSTALL_CLI=true bash" >&2
        echo "" >&2
    fi

    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" >&2
}

#######################################
# Main installation function
#######################################
main() {
    echo "" >&2
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" >&2
    echo "  Replication Manager Installation Script" >&2
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" >&2
    echo "" >&2

    # Detect platform
    info "Detecting platform..."
    DETECTED_OS=$(detect_os)
    DETECTED_ARCH=$(detect_arch)

    if [ "$DETECTED_OS" = "unsupported" ]; then
        error_exit "Unsupported operating system: $(uname -s). Only Linux and macOS are supported."
    fi

    if [ "$DETECTED_ARCH" = "unsupported" ]; then
        error_exit "Unsupported architecture: $(uname -m). Only amd64 and arm64 are supported."
    fi

    success "Detected platform: ${DETECTED_OS}-${DETECTED_ARCH}"
    echo "" >&2

    # Get version
    if [ -z "$VERSION" ]; then
        VERSION=$(get_latest_version)
    fi
    info "Target version: ${VERSION}"
    echo "" >&2

    # Determine installation directory
    local install_dir
    install_dir=$(determine_install_dir)
    info "Installation directory: ${install_dir}"

    if [ "$install_dir" = "$HOME/.local/bin" ]; then
        warning "Installing to user directory (no sudo privileges detected)"
    fi
    echo "" >&2

    # Create temporary directory
    TEMP_DIR=$(mktemp -d -t repman-install.XXXXXXXXXX)

    # Download and install server binary
    local server_tarball="${TEMP_DIR}/replication-manager.tar.gz"
    download_binary "$VERSION" "$DETECTED_OS" "$DETECTED_ARCH" "server" "$server_tarball"

    local extracted_binary
    extracted_binary=$(extract_binary "$server_tarball" "$TEMP_DIR")

    install_binary "$extracted_binary" "$install_dir" "replication-manager"
    echo "" >&2

    # Verify server installation
    verify_installation "${install_dir}/replication-manager"
    echo "" >&2

    # Install CLI if requested
    local cli_installed="false"
    if [ "$INSTALL_CLI" = "true" ]; then
        local cli_tarball="${TEMP_DIR}/replication-manager-cli.tar.gz"
        download_binary "$VERSION" "$DETECTED_OS" "$DETECTED_ARCH" "cli" "$cli_tarball"

        local cli_binary
        cli_binary=$(extract_binary "$cli_tarball" "$TEMP_DIR")

        install_binary "$cli_binary" "$install_dir" "replication-manager-cli"
        echo "" >&2

        verify_installation "${install_dir}/replication-manager-cli"
        echo "" >&2

        cli_installed="true"
    fi

    # Print post-installation information
    print_post_install_info "$install_dir" "$VERSION" "$cli_installed"
}

# Run main function
main "$@"
