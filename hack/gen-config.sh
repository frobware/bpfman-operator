#!/bin/bash

volume_paths=$(nix-distrobox-print-volume-paths)

# Function to sanitize names to conform with RFC 1123
sanitize_name() {
    echo "$1" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed -E 's/^-+|-+$//g'
}

# Initial part of the KIND configuration
cat << EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraMounts:
EOF

# Iterate over each volume path and append to the KIND config
for path in $volume_paths; do
    host_path=$(echo $path | cut -d':' -f1)
    container_path=$(echo $path | cut -d':' -f2)
    sanitized_host_path=$(sanitize_name $host_path)

    cat << EOF
    - hostPath: $host_path
      containerPath: $container_path
EOF
done

# Add the user's home directory to the KIND config
cat << EOF
    - hostPath: /home/\${USER}
      containerPath: /home/\${USER}
EOF
