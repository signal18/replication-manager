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

# Check if the .ssh directory already exists for the repman user
if [ ! -d /home/repman/.ssh ]; then
    # Create the .ssh directory if it does not exist
    echo "Creating .ssh directory for user 'repman'..."
    mkdir -p /home/repman/.ssh
    chmod 700 /home/repman/.ssh
    chown repman:repman /home/repman/.ssh
fi

# Check if the authorized_keys file exists
if [ ! -f /home/repman/.ssh/authorized_keys ]; then
    # Copy the .ssh directory from root if no keys exist
    if [ -d /root/.ssh ]; then
        echo "Copying SSH configuration from /root/.ssh to /home/repman/.ssh..."
        cp -r /root/.ssh/* /home/repman/.ssh/
        chmod 700 /home/repman/.ssh
        chmod 600 /home/repman/.ssh/authorized_keys
        chown -R repman:repman /home/repman/.ssh
    else
        echo "No SSH configuration found in /root/.ssh."
    fi
else
    echo ".ssh directory and authorized_keys already exist for user 'repman'. Skipping copy."
fi
