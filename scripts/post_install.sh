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
    chown repman:repman /home/repman/.ssh
fi

# Check and copy private keys from /root/.ssh to /home/repman/.ssh (RSA, ECDSA, ED25519)
declare -a key_types=("id_rsa" "id_ecdsa" "id_ed25519")
for key in "${key_types[@]}"; do
    if [ -f /root/.ssh/$key ]; then
        echo "Copying $key from /root/.ssh to /home/repman/.ssh/$key..."
        cp /root/.ssh/$key /home/repman/.ssh/
        chmod 600 /home/repman/.ssh/$key
        chown repman:repman /home/repman/.ssh/$key
    else
        echo "No $key found in /root/.ssh."
    fi
done

# Check and copy corresponding public keys from /root/.ssh to /home/repman/.ssh (RSA, ECDSA, ED25519)
for key in "${key_types[@]}"; do
    pub_key="${key}.pub"
    if [ -f /root/.ssh/$pub_key ]; then
        echo "Copying $pub_key from /root/.ssh to /home/repman/.ssh/$pub_key..."
        cp /root/.ssh/$pub_key /home/repman/.ssh/
        chmod 644 /home/repman/.ssh/$pub_key
        chown repman:repman /home/repman/.ssh/$pub_key
    else
        echo "No $pub_key found in /root/.ssh."
    fi
done

# Ensure necessary directories for the application exist
echo "Creating directory /var/lib/replication-manager if it doesn't exist..."
if [ ! -d /var/lib/replication-manager ]; then
    mkdir -p /var/lib/replication-manager
else
    echo "Directory /var/lib/replication-manager already exists."
fi

# Set ownership to repman:repman
echo "Setting ownership of /var/lib/replication-manager to repman:repman..."
chown repman:repman /var/lib/replication-manager

# Set appropriate permissions to 755 (owner read/write/execute, group/others read/execute)
chmod 755 /var/lib/replication-manager