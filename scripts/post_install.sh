#!/bin/bash

# Check if the group already exists
if ! getent group repman > /dev/null; then
    echo "Creating group 'repman'..."
    groupadd repman
else
    echo "Group 'repman' already exists."
fi

# Check if the user already exists
if ! id "repman" &>/dev/null; then
    # Create a regular user with a home directory, bash as the shell, and add to 'repman' group
    echo "Creating user 'repman' with home directory and adding to group 'repman'..."
    useradd -m -d /home/repman -s /bin/bash -g repman repman
else
    echo "User 'repman' already exists."
fi

# Ensure the .ssh directory exists for the repman user
if [ ! -d /home/repman/.ssh ]; then
    echo "Creating .ssh directory for user 'repman'..."
    mkdir -p /home/repman/.ssh
    chmod 700 /home/repman/.ssh
    chown -R repman:repman /home/repman/.ssh
fi

# SSH keys are NOT copied from root. If remote scripting or configurator
# needs SSH access, generate a dedicated key for the repman user:
#   sudo -u repman ssh-keygen -t ed25519 -f /home/repman/.ssh/id_ed25519 -N ""
# Then distribute the public key to your database servers.
# See: https://docs.signal18.io/installation/setup-instructions

# Ensure necessary directories for the application exist
echo "Creating directory /var/lib/replication-manager if it doesn't exist..."
if [ ! -d /var/lib/replication-manager ]; then
    mkdir -p /var/lib/replication-manager
else
    echo "Directory /var/lib/replication-manager already exists."
fi

# Set ownership to repman:repman
echo "Setting ownership of /var/lib/replication-manager to repman:repman..."
chown -R repman:repman /var/lib/replication-manager

# Set appropriate permissions to 755 (owner read/write/execute, group/others read/execute)
chmod 755 /var/lib/replication-manager

# Create /usr/share/replication-manager if it doesn't exist
echo "Creating directory /usr/share/replication-manager if it doesn't exist..."
if [ ! -d /usr/share/replication-manager ]; then
    mkdir -p /usr/share/replication-manager
else
    echo "Directory /usr/share/replication-manager already exists."
fi

# Set ownership to repman:repman
echo "Setting ownership of /usr/share/replication-manager to repman:repman..."
chown -R repman:repman /usr/share/replication-manager

# Create /var/log/replication-manager if it doesn't exist
echo "Creating directory /var/log/replication-manager if it doesn't exist..."
if [ ! -d /var/log/replication-manager ]; then
    mkdir -p /var/log/replication-manager
else
    echo "Directory /var/log/replication-manager already exists."
fi

# Set ownership to repman:repman
echo "Setting ownership of /var/log/replication-manager to repman:repman..."
chown -R repman:repman /var/log/replication-manager
