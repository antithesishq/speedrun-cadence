# Instrumented client/worker program (Cadence Go client v1.4.0). Build context: antithesis/client
FROM golang:1.26-trixie AS build
ENV CGO_ENABLED=1
RUN go install github.com/antithesishq/antithesis-sdk-go/tools/antithesis-go-toolexec@v0.9.0
RUN cd /tmp && GOFLAGS=-mod=mod go mod download -json github.com/antithesishq/antithesis-sdk-go@v0.9.0 > sdk.json && \
    cp -r "$(sed -n 's/.*"Dir": "\(.*\)".*/\1/p' sdk.json)" /antithesis-sdk-go && chmod -R u+w /antithesis-sdk-go
ENV ANTITHESIS_SDK_MODULE_DIR=/antithesis-sdk-go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# Instrument our program and the Cadence client library (the library is code under test too).
RUN ANTITHESIS_INSTRUMENT=cadence-antithesis/client,go.uber.org/cadence ANTITHESIS_DISPLAY_NAME=cadence-client \
    go build -toolexec=antithesis-go-toolexec -o /out/client . && \
    echo "init_coverage: $(go tool nm /out/client | grep -c 'voidstarHandler).init_coverage')"

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates socat jq procps && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/client /usr/local/bin/client
COPY --from=build /symbols /symbols
RUN mkdir -p /clients
CMD ["/usr/local/bin/client"]
