FROM mcr.microsoft.com/devcontainers/go:1.25

# Remove the Yarn repo that has an expired/missing GPG key
RUN rm -f /etc/apt/sources.list.d/yarn.list || true

RUN apt-get update && apt-get install -y --no-install-recommends \
    kmod \
    iproute2 \
    iputils-ping \
    ethtool \
    tcpdump \
    jq \
    ca-certificates \
    bash-completion \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /work
