# Instrumented Cadence server image. Build context: a Cadence source checkout.
# Named context "harness" = antithesis/server (entrypoint scripts).
FROM golang:1.26-trixie AS build
ENV GOTOOLCHAIN=auto CGO_ENABLED=1
RUN go install github.com/antithesishq/antithesis-sdk-go/tools/antithesis-go-toolexec@v0.9.0
WORKDIR /cadence
COPY go.* ./
COPY cmd/server/go.* ./cmd/server/
COPY common/archiver/gcloud/go.* ./common/archiver/gcloud/
COPY common/dynamicconfig/openfeatureprovider/unleash/go.* ./common/dynamicconfig/openfeatureprovider/unleash/
COPY common/persistence/sql/sqlplugin/cloudsql-mysql/go.* ./common/persistence/sql/sqlplugin/cloudsql-mysql/
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download
COPY . .
RUN rm -rf .bin .build idls
# Instrumentation needs the SDK source; point the toolexec at a copy (no change to Cadence's go.mod).
RUN cd /tmp && GOFLAGS=-mod=mod go mod download -json github.com/antithesishq/antithesis-sdk-go@v0.9.0 > sdk.json && \
    cp -r "$(sed -n 's/.*"Dir": "\(.*\)".*/\1/p' sdk.json)" /antithesis-sdk-go && chmod -R u+w /antithesis-sdk-go
ENV ANTITHESIS_SDK_MODULE_DIR=/antithesis-sdk-go
# Instrumented server (only Cadence's own packages: the default instruments the working tree).
RUN --mount=type=cache,target=/root/go/pkg/mod \
    ANTITHESIS_SKIP_TEST_FILES=true ANTITHESIS_DISPLAY_NAME=cadence-server \
    go build -toolexec=antithesis-go-toolexec -o /out/cadence-server ./cmd/server && \
    echo "init_coverage symbols: $(go tool nm /out/cadence-server | grep -c 'voidstarHandler).init_coverage')"
# Tools: plain builds (not part of the SUT under test).
RUN --mount=type=cache,target=/root/go/pkg/mod \
    go build -o /out/cadence-sql-tool ./cmd/tools/sql && \
    go build -o /out/cadence ./cmd/tools/cli

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq postgresql-client procps iproute2 netcat-openbsd tzdata && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
COPY --from=build /symbols /symbols
COPY --from=build /cadence/schema /etc/cadence/schema
COPY --from=harness entrypoint.sh setup-schema.sh /usr/local/bin/
RUN chmod 755 /usr/local/bin/*.sh && mkdir -p /etc/cadence/config
WORKDIR /etc/cadence
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
