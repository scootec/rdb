# Stage 1: Build Go binary
FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /rdb ./cmd/rdb

# Stage 2: Runtime with restic
FROM restic/restic:0.19.1
COPY --from=builder /rdb /usr/local/bin/rdb
HEALTHCHECK --interval=5m --timeout=10s --start-period=1m CMD ["rdb", "health"]
ENTRYPOINT ["rdb"]
CMD ["run"]
