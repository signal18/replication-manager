# Hosting Installation Script on signal18.io

This document provides instructions for hosting the `install.sh` script on signal18.io to enable one-liner installation via:

```bash
curl -fsSL https://signal18.io/get-repman | bash
```

## Table of Contents

- [Prerequisites](#prerequisites)
- [Web Server Configuration](#web-server-configuration)
  - [Nginx](#nginx-configuration)
  - [Apache](#apache-configuration)
- [Deployment Steps](#deployment-steps)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)
- [Security Considerations](#security-considerations)
- [Maintenance](#maintenance)

## Prerequisites

Before hosting the installation script, ensure you have:

1. **Domain Access**: Control over signal18.io DNS settings
2. **Web Server**: nginx or Apache installed and configured
3. **SSL Certificate**: Valid SSL certificate (Let's Encrypt recommended)
4. **Server Access**: SSH access with appropriate permissions

## Web Server Configuration

### Nginx Configuration

Add the following configuration to your nginx server block:

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name signal18.io www.signal18.io;

    # Redirect HTTP to HTTPS
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name signal18.io www.signal18.io;

    # SSL configuration
    ssl_certificate /etc/letsencrypt/live/signal18.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/signal18.io/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers on;
    ssl_ciphers ECDHE-RSA-AES256-GCM-SHA512:DHE-RSA-AES256-GCM-SHA512:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES256-GCM-SHA384;

    # Install script endpoint
    location = /get-repman {
        alias /var/www/signal18.io/scripts/install.sh;
        default_type text/plain;
        add_header Content-Type "text/plain; charset=utf-8";
        add_header X-Content-Type-Options "nosniff";
        add_header Cache-Control "public, max-age=300"; # Cache for 5 minutes
    }

    # Optional: Redirect root to GitHub repository
    location = / {
        return 302 https://github.com/signal18/replication-manager;
    }

    # Other site configuration...
}
```

**Configuration Notes:**
- The `alias` directive maps the URL path to the filesystem path
- `default_type text/plain` ensures the script is served as plain text
- `X-Content-Type-Options: nosniff` prevents MIME type sniffing
- `Cache-Control` allows caching for better performance while keeping updates reasonable

**Apply Configuration:**
```bash
# Test configuration
sudo nginx -t

# Reload nginx
sudo systemctl reload nginx
```

### Apache Configuration

Add the following to your Apache virtual host configuration:

```apache
<VirtualHost *:80>
    ServerName signal18.io
    ServerAlias www.signal18.io

    # Redirect HTTP to HTTPS
    Redirect permanent / https://signal18.io/
</VirtualHost>

<VirtualHost *:443>
    ServerName signal18.io
    ServerAlias www.signal18.io

    # SSL configuration
    SSLEngine on
    SSLCertificateFile /etc/letsencrypt/live/signal18.io/fullchain.pem
    SSLCertificateKeyFile /etc/letsencrypt/live/signal18.io/privkey.pem
    SSLProtocol all -SSLv2 -SSLv3 -TLSv1 -TLSv1.1
    SSLCipherSuite HIGH:!aNULL:!MD5

    # Install script endpoint
    Alias /get-repman /var/www/signal18.io/scripts/install.sh
    <Location /get-repman>
        ForceType text/plain
        Header set Content-Type "text/plain; charset=utf-8"
        Header set X-Content-Type-Options "nosniff"
        Header set Cache-Control "public, max-age=300"
    </Location>

    <Directory /var/www/signal18.io/scripts>
        Require all granted
        Options -Indexes
    </Directory>

    # Other site configuration...
</VirtualHost>
```

**Required Apache Modules:**
```bash
sudo a2enmod ssl
sudo a2enmod headers
sudo a2enmod alias
```

**Apply Configuration:**
```bash
# Test configuration
sudo apachectl configtest

# Reload Apache
sudo systemctl reload apache2
```

## Deployment Steps

### 1. Prepare the Script

```bash
# Clone or pull the latest repository
cd /tmp
git clone https://github.com/signal18/replication-manager.git
# or
cd replication-manager && git pull

# Copy the script to the web server directory
sudo mkdir -p /var/www/signal18.io/scripts
sudo cp replication-manager/scripts/install.sh /var/www/signal18.io/scripts/
```

### 2. Set Permissions

```bash
# Set ownership (adjust user:group as needed)
sudo chown www-data:www-data /var/www/signal18.io/scripts/install.sh

# Set read permissions
sudo chmod 644 /var/www/signal18.io/scripts/install.sh

# Verify permissions
ls -la /var/www/signal18.io/scripts/install.sh
```

Expected output:
```
-rw-r--r-- 1 www-data www-data 15234 Dec 30 12:00 /var/www/signal18.io/scripts/install.sh
```

### 3. Configure Web Server

Choose either Nginx or Apache configuration from the sections above and apply it.

### 4. Verify DNS

Ensure DNS records point to your server:

```bash
# Check A record
dig +short signal18.io

# Check AAAA record (IPv6)
dig +short AAAA signal18.io
```

### 5. SSL Certificate

If you don't have an SSL certificate, obtain one using Let's Encrypt:

```bash
# Install certbot (Debian/Ubuntu)
sudo apt-get install certbot python3-certbot-nginx
# or for Apache
sudo apt-get install certbot python3-certbot-apache

# Obtain certificate (nginx)
sudo certbot --nginx -d signal18.io -d www.signal18.io

# Obtain certificate (Apache)
sudo certbot --apache -d signal18.io -d www.signal18.io
```

## Testing

### 1. Basic Connectivity Test

```bash
# Test HTTP to HTTPS redirect
curl -I http://signal18.io/get-repman

# Expected: 301 or 302 redirect to HTTPS
```

### 2. HTTPS Content Test

```bash
# Fetch the script via HTTPS
curl -fsSL https://signal18.io/get-repman | head -n 5

# Expected: First few lines of the bash script
```

### 3. Content-Type Header Test

```bash
curl -I https://signal18.io/get-repman

# Expected headers:
# HTTP/2 200
# content-type: text/plain; charset=utf-8
# x-content-type-options: nosniff
```

### 4. Dry Run Installation Test

```bash
# Download and check syntax without installing
curl -fsSL https://signal18.io/get-repman | bash -n

# No output = valid bash syntax
```

### 5. Full Installation Test (Non-Destructive)

```bash
# Install to temporary directory
curl -fsSL https://signal18.io/get-repman | REPMAN_INSTALL_DIR=/tmp/repman-test bash

# Verify installation
/tmp/repman-test/replication-manager version

# Cleanup
rm -rf /tmp/repman-test
```

### Testing Checklist

- [ ] HTTP redirects to HTTPS (301/302 status)
- [ ] HTTPS serves script content successfully (200 status)
- [ ] Content-Type header is `text/plain; charset=utf-8`
- [ ] X-Content-Type-Options header is `nosniff`
- [ ] Script has valid bash syntax (bash -n check passes)
- [ ] Script can download from GitHub
- [ ] Script installs binary successfully
- [ ] Installed binary is functional (version command works)

## Troubleshooting

### Issue: 404 Not Found

**Cause**: Script file doesn't exist or path is incorrect

**Solution**:
```bash
# Check if file exists
sudo ls -la /var/www/signal18.io/scripts/install.sh

# Check web server error logs
sudo tail -f /var/log/nginx/error.log
# or
sudo tail -f /var/log/apache2/error.log
```

### Issue: 403 Forbidden

**Cause**: Incorrect file permissions

**Solution**:
```bash
# Fix file permissions
sudo chmod 644 /var/www/signal18.io/scripts/install.sh

# Fix directory permissions
sudo chmod 755 /var/www/signal18.io/scripts
```

### Issue: Certificate Errors

**Cause**: SSL certificate invalid or expired

**Solution**:
```bash
# Check certificate expiry
openssl s_client -connect signal18.io:443 -servername signal18.io 2>/dev/null | openssl x509 -noout -dates

# Renew Let's Encrypt certificate
sudo certbot renew
```

### Issue: Script Fails to Execute

**Cause**: Incorrect line endings (CRLF instead of LF) or missing shebang

**Solution**:
```bash
# Check file format
file /var/www/signal18.io/scripts/install.sh

# Convert line endings if needed (install dos2unix)
sudo apt-get install dos2unix
sudo dos2unix /var/www/signal18.io/scripts/install.sh
```

## Security Considerations

### 1. Content Security

- Always serve the script over HTTPS
- Use `X-Content-Type-Options: nosniff` header to prevent MIME sniffing
- Regularly update the script from the repository

### 2. Access Logging

Enable access logging to monitor script usage:

**Nginx:**
```nginx
access_log /var/log/nginx/get-repman.log;
```

**Apache:**
```apache
CustomLog /var/log/apache2/get-repman.log combined
```

### 3. Rate Limiting

Consider implementing rate limiting to prevent abuse:

**Nginx:**
```nginx
limit_req_zone $binary_remote_addr zone=repman:10m rate=10r/m;

location = /get-repman {
    limit_req zone=repman burst=5;
    # ... rest of configuration
}
```

### 4. Integrity Verification

Consider implementing subresource integrity (SRI) or providing checksums for users who want to verify the script before execution.

## Maintenance

### Regular Updates

Set up a process to regularly update the script from the repository:

```bash
#!/bin/bash
# /usr/local/bin/update-repman-installer.sh

REPO_DIR="/tmp/replication-manager"
SCRIPT_DEST="/var/www/signal18.io/scripts/install.sh"

# Clone or update repository
if [ -d "$REPO_DIR" ]; then
    cd "$REPO_DIR" && git pull
else
    git clone https://github.com/signal18/replication-manager.git "$REPO_DIR"
fi

# Copy script
sudo cp "$REPO_DIR/scripts/install.sh" "$SCRIPT_DEST"
sudo chown www-data:www-data "$SCRIPT_DEST"
sudo chmod 644 "$SCRIPT_DEST"

echo "Installer script updated successfully"
```

Add to crontab for automatic updates:
```bash
# Update installer script daily at 3 AM
0 3 * * * /usr/local/bin/update-repman-installer.sh
```

### Monitoring

Monitor the following:
- SSL certificate expiration (setup certbot auto-renewal)
- Web server access logs for errors
- Script execution success rate
- GitHub API rate limits (if applicable)

### Backup

Keep a backup of the working script:
```bash
sudo cp /var/www/signal18.io/scripts/install.sh /var/www/signal18.io/scripts/install.sh.bak
```

## Support

For issues or questions:
- GitHub Issues: https://github.com/signal18/replication-manager/issues
- Repository: https://github.com/signal18/replication-manager

## License

This documentation and the installation script are part of the replication-manager project and follow the same license terms.
