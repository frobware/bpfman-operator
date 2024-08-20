FROM quay.io/fedora/fedora:40

# This is a build-time variable and must be present in the
# environment.
ARG GITHUB_USER=frobware

ARG CLEAN_DNF_CACHE=false
ARG DNF_UPDATE=false

RUN INSTALL_PKGS="procps-ng lsof strace rsync wget tini openssh-server golang" && \
    [ "$DNF_UPDATE" = "true" ] && dnf update -y || echo "Skipping dnf update" && \
    dnf install -y $INSTALL_PKGS $HAPROXY_RPMS && \
    [ "$CLEAN_DNF_CACHE" = "true" ] && dnf clean all || echo "Skipping dnf clean all"

RUN echo "AllowAgentForwarding no" >> /etc/ssh/sshd_config && \
    echo "PasswordAuthentication no" >> /etc/ssh/sshd_config && \
    echo "PermitRootLogin yes" >> /etc/ssh/sshd_config && \
    echo "Port 2222" >> /etc/ssh/sshd_config && \
    echo "Match User root" >> /etc/ssh/sshd_config && \
    echo "  AuthorizedKeysFile /etc/ssh/authorized_keys.d/%u" >> /etc/ssh/sshd_config && \
    ssh-keygen -A && \
    mkdir -p /etc/ssh/authorized_keys.d && \
    curl -s https://github.com/${GITHUB_USER}.keys -o /etc/ssh/authorized_keys.d/root && \
    chmod 755 /etc/ssh/authorized_keys.d && \
    chmod 444 /etc/ssh/authorized_keys.d/root

COPY env-helper /env-helper

# WORKDIR /src
# COPY config/dev/env-helper.go env-helper.go
# RUN CGO_ENABLED=0 go mod init env-helper
# RUN CGO_ENABLED=0 go mod tidy
# RUN CGO_ENABLED=0 go build -o /env-helper env-helper.go

#ENTRYPOINT ["/usr/bin/tini", "-v", "-s", "--", "/usr/sbin/sshd", "-D", "-E", "/proc/1/fd/1"]
ENTRYPOINT ["/usr/bin/tini", "-v", "-s", "--", "/env-helper"]
