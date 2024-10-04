#!/bin/bash

# Check if the group already exists
if ! getent group repman > /dev/null; then
    echo "Creating group 'repman'..."
    groupadd repman
else
    echo "Group 'repman' already exists."
fi

# Check if the user already exists
if id "repman" &>/dev/null; then
    echo "User 'repman' already exists."
else
    # Create a regular user with a home directory, bash as the shell, and add to 'repman' group
    echo "Creating user 'repman' with home directory and adding to group 'repman'..."
    useradd -m -d /home/repman -s /bin/bash -g repman repman
    
    # Check if the .ssh directory already exists for the repman user
    if [ ! -d /home/repman/.ssh ]; then
        # Copy the .ssh directory from root if it does not exist
        if [ -d /root/.ssh ]; then
            echo "Copying SSH configuration from /root/.ssh to /home/repman/.ssh..."
            cp -r /root/.ssh /home/repman/.ssh
            chmod 700 /home/repman/.ssh
            chmod 600 /home/repman/.ssh/authorized_keys
            chown -R repman:repman /home/repman/.ssh
        else
            echo "No SSH configuration found in /root/.ssh. Creating empty .ssh directory..."
            mkdir -p /home/repman/.ssh
            chmod 700 /home/repman/.ssh
            touch /home/repman/.ssh/authorized_keys
            chmod 600 /home/repman/.ssh/authorized_keys
            chown -R repman:repman /home/repman/.ssh
        fi
    else
        echo ".ssh directory already exists for user 'repman'. Skipping copy."
    fi
fi