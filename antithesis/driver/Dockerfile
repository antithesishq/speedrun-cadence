FROM golang:1.26-trixie AS build
ENV CGO_ENABLED=1
RUN go install github.com/antithesishq/antithesis-sdk-go/tools/antithesis-go-toolexec@v0.9.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN ANTITHESIS_DISPLAY_NAME=cadence-driver go build -toolexec=antithesis-go-toolexec -o /out/driver . && \
    echo "init_coverage: $(go tool nm /out/driver | grep -c 'voidstarHandler).init_coverage')"

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates socat jq procps && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/driver /usr/local/bin/driver
COPY --from=build /symbols /symbols
COPY first_start_workload /opt/antithesis/test/v1/main/first_start_workload
RUN chmod 755 /opt/antithesis/test/v1/main/first_start_workload && mkdir -p /clients
CMD ["/usr/local/bin/driver"]
