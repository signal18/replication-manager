#!/bin/bash
# Script to clean up old/incorrect Docker Hub tags
# Usage: ./cleanup-docker-tags.sh <docker-hub-token>

set -e

# Configuration
DOCKER_USER="signal18"
DOCKER_REPO="replication-manager"
TAGS_TO_DELETE=("2.3" "2.3-pro" "2.3-dev")

# Check if token is provided
if [ -z "$1" ]; then
    echo "Error: Docker Hub token required"
    echo "Usage: $0 <docker-hub-token>"
    echo ""
    echo "To get a token:"
    echo "1. Go to https://hub.docker.com/settings/security"
    echo "2. Click 'New Access Token'"
    echo "3. Give it a description (e.g., 'cleanup-script')"
    echo "4. Set permissions to 'Read, Write, Delete'"
    echo "5. Copy the generated token"
    exit 1
fi

DOCKER_TOKEN="$1"

echo "=========================================="
echo "Docker Hub Tag Cleanup Script"
echo "=========================================="
echo "Repository: ${DOCKER_USER}/${DOCKER_REPO}"
echo "Tags to delete: ${TAGS_TO_DELETE[@]}"
echo ""

# Function to delete a tag
delete_tag() {
    local tag="$1"
    echo -n "Deleting tag '${tag}'... "

    response=$(curl -s -w "\n%{http_code}" -X DELETE \
        -H "Authorization: Bearer ${DOCKER_TOKEN}" \
        "https://hub.docker.com/v2/repositories/${DOCKER_USER}/${DOCKER_REPO}/tags/${tag}/")

    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | head -n-1)

    if [ "$http_code" = "204" ] || [ "$http_code" = "200" ]; then
        echo "✓ SUCCESS"
        return 0
    elif [ "$http_code" = "404" ]; then
        echo "⚠ NOT FOUND (may have already been deleted)"
        return 0
    else
        echo "✗ FAILED (HTTP $http_code)"
        echo "Response: $body"
        return 1
    fi
}

# Verify token works by listing tags first
echo "Verifying Docker Hub access..."
verify_response=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer ${DOCKER_TOKEN}" \
    "https://hub.docker.com/v2/repositories/${DOCKER_USER}/${DOCKER_REPO}/tags/?page_size=1")

verify_code=$(echo "$verify_response" | tail -n1)

if [ "$verify_code" != "200" ]; then
    echo "✗ ERROR: Unable to access Docker Hub API"
    echo "HTTP Status: $verify_code"
    echo "Please check your token and try again"
    exit 1
fi
echo "✓ Access verified"
echo ""

# Delete each tag
echo "Starting tag deletion..."
echo ""

failed_tags=()
for tag in "${TAGS_TO_DELETE[@]}"; do
    if ! delete_tag "$tag"; then
        failed_tags+=("$tag")
    fi
done

echo ""
echo "=========================================="
echo "Cleanup Summary"
echo "=========================================="
echo "Total tags processed: ${#TAGS_TO_DELETE[@]}"

if [ ${#failed_tags[@]} -eq 0 ]; then
    echo "✓ All tags successfully deleted"
    exit 0
else
    echo "✗ Failed tags: ${failed_tags[@]}"
    exit 1
fi
