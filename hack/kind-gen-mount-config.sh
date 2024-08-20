#!/usr/bin/env bash

# This script is designed to generate Kubernetes configuration files,
# specifically for KIND (Kubernetes IN Docker) clusters and
# DaemonSets. The primary purpose is to mount the user's home
# directory and any relevant Nix-related paths from the host into the
# container. This is particularly useful for users running NixOS or
# using Nix package management, as it allows necessary paths to be
# accessed inside the Kubernetes environment.
#
# The script supports mounting paths as read-only or read-write,
# depending on the specified configuration. It also includes a
# `--verbose` option for detailed output during execution.
#
# Notably, we set the host's PATH value as KIND_HOST_PATH in the
# daemonset. This allows you to add:
#
#   export PATH=$PATH${KIND_HOST_PATH:+:$KIND_HOST_PATH}
#
# to your shell initialisation (e.g., ~/.bash_profile) which will make
# your host's tool available in the container.

# Function to sanitise names to conform with RFC 1123, ensuring the
# name is lowercase, contains only letters, digits, and hyphens, and
# does not start or end with a hyphen. This is necessary for
# compatibility with Kubernetes naming conventions.
sanitise_name() {
    echo "$1" | tr '[:upper:]' '[:lower:]' | tr '/' '-' | tr -c 'a-z0-9-' '-' | sed -E 's/^-+|-+$//g'
}

# Function to check if a path exists or is a valid symlink, and add it to volume_args.
process_and_add_path() {
    local path="$1"
    local mount_options="$2"
    local -n volume_args_ref=$3

    # Verbose output
    if [ "$verbose" = true ]; then
        echo "Checking path: $path" >&2
    fi

    # Verify the path or symlink
    if [ -e "$path" ]; then
        volume_args_ref+=("$path:$mount_options")
    elif [ -L "$path" ]; then
        local real_target
        real_target=$(realpath "$path")
        if [ -e "$real_target" ]; then
            volume_args_ref+=("$path:$mount_options")
        else
            echo "Warning: Symlink $path points to a non-existent target $real_target." >&2
        fi
    else
        echo "Warning: Directory or symlink $path does not exist and will not be included." >&2
    fi
}


# Main function to generate either kind or daemonset configuration.
generate_config() {
    local mode="$1"
    shift # Remove mode from arguments
    local volume_args=()

    # Default paths to check with mount options.
    local user_id_path="/run/user/$(id -u)"
    local paths_to_check=()

    # Static paths with their mount options
    declare -A static_paths=(
        ["/nix/store"]="ro"
        ["/etc/profiles"]="ro"
        ["/etc/static"]="ro"
        ["$user_id_path"]="rw"
        ["$HOME"]="rw"
    )

    # Add static paths only if they exist
    for path in "${!static_paths[@]}"; do
        if [ -e "$path" ]; then
            paths_to_check+=("$path:${static_paths[$path]}")
        else
            if [ "$verbose" = true ]; then
                echo "Skipping non-existent path: $path" >&2
            fi
        fi
    done

    # Add paths from NIX_PROFILES as read-only.
    if [ -n "$NIX_PROFILES" ]; then
        for profile_path in $NIX_PROFILES; do
            if [ -e "$profile_path" ]; then
                paths_to_check+=("$profile_path:ro")
            else
                if [ "$verbose" = true ]; then
                    echo "Skipping non-existent NIX profile path: $profile_path" >&2
                fi
            fi
        done
    fi

    # Add any remaining command line arguments as paths to check with default rw.
    if [ "$#" -gt 0 ]; then
        for extra_path in "$@"; do
            paths_to_check+=("$extra_path:rw")
        done
    fi

    # Process all paths and add valid ones to volume_args.
    for path_option in "${paths_to_check[@]}"; do
        IFS=":" read -r path option <<< "$path_option"
        process_and_add_path "$path" "$option" volume_args
    done

    # Sort and remove duplicates from the volume_args array.
    volume_args=($(printf "%s\n" "${volume_args[@]}" | sort | uniq))

    if [ "$mode" == "kind" ]; then
        # KIND configuration output
        cat << EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraMounts:
EOF

        for path_option in "${volume_args[@]}"; do
            IFS=":" read -r path _ <<< "$path_option"
            cat << EOF
    - hostPath: $path
      containerPath: $path
EOF
        done

    elif [ "$mode" == "daemonset" ]; then
        # DaemonSet configuration output
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
        env:
        - name: KIND_HOST_PATH
          value: "$(echo $PATH)"
        volumeMounts:
EOF

        for path_option in "${volume_args[@]}"; do
            IFS=":" read -r path option <<< "$path_option"
            volume_name=$(sanitise_name "$path")

            cat << EOF
        - name: $volume_name
          mountPath: $path
          readOnly: $( [ "$option" == "ro" ] && echo "true" || echo "false" )
EOF
        done

        cat << EOF
      volumes:
EOF

        for path_option in "${volume_args[@]}"; do
            IFS=":" read -r path _ <<< "$path_option"
            volume_name=$(sanitise_name "$path")

            cat << EOF
      - name: $volume_name
        hostPath:
          path: $path
          type: Directory
EOF
        done

    else
        echo "Usage: $0 [--verbose] kind|daemonset [additional paths...]"
        exit 1
    fi
}

# Parse arguments
verbose=false
mode=""

# Check for --verbose flag and mode
for arg in "$@"; do
    if [ "$arg" == "--verbose" ]; then
        verbose=true
        shift
    elif [ -z "$mode" ] && [[ "$arg" == "kind" || "$arg" == "daemonset" ]]; then
        mode="$arg"
        shift
    else
        break
    fi
done

if [ -z "$mode" ]; then
    echo "Mode not specified. Usage: $0 [--verbose] kind|daemonset [additional paths...]"
    exit 1
fi

generate_config "$mode" "$@"
