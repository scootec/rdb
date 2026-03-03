FROM mcr.microsoft.com/devcontainers/go:1.24

# Install restic for local testing
RUN apt-get update && apt-get install -y --no-install-recommends \
    restic \
    && rm -rf /var/lib/apt/lists/*
