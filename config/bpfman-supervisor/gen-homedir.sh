#!/bin/bash

volume_paths=$(nix-distrobox-print-volume-paths)

# Function to sanitize volume names to conform with RFC 1123 and replace '/' with '-'
sanitize_name() {
    echo "$1" | tr '[:upper:]' '[:lower:]' | tr '/' '-' | tr -c 'a-z0-9-' '-' | sed -E 's/^-+|-+$//g'
}

# Generate DaemonSet configuration
cat << EOF
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: daemon
  namespace: kube-system
spec:
  template:
    spec:
      containers:
      - name: bpfman-supervisor
        volumeMounts:
EOF

# Iterate over each volume path and append to the DaemonSet config
for path in $volume_paths; do
  host_path=$(echo $path | cut -d':' -f1)
  container_path=$(echo $path | cut -d':' -f2)
  volume_name=$(sanitize_name $host_path)

  # Print volumeMounts to standard output
  cat << EOF
        - name: $volume_name
          mountPath: $container_path
          readOnly: true
EOF
done

# Add the home directory mount
cat << EOF
        - name: home-dir
          mountPath: /home/\${USER}
EOF

# Append volumes section to DaemonSet config
cat << EOF
      volumes:
EOF

# Iterate again for volumes and append to standard output
for path in $volume_paths; do
  host_path=$(echo $path | cut -d':' -f1)
  volume_name=$(sanitize_name $host_path)

  # Print volumes to standard output
  cat << EOF
      - name: $volume_name
        hostPath:
          path: $host_path
          type: Directory
EOF
done

# Add the home directory volume
cat << EOF
      - name: home-dir
        hostPath:
          path: /home/\${USER}
          type: Directory
EOF
