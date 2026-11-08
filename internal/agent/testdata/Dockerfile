# The image `bin/dev test --agent` runs the agent's tests in: the tools the
# agent calls (nft, dnsmasq), ip for the tests, and busybox's udhcpc as a
# server asking for its address.
FROM debian:trixie-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    busybox dnsmasq-base iproute2 nftables \
  && rm -rf /var/lib/apt/lists/*
