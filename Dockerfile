# ---------------------------------------------------------------- build stage
# CGO is required by the SQLite driver, so the builder needs a C toolchain and
# the runtime image needs a matching libc. Debian slim is used on both sides to
# keep that pairing simple and avoid musl surprises.
FROM golang:1.22-bookworm AS build

WORKDIR /src

# Dependencies first so this layer is cached across source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The web assets and the seed sheet are compiled into the binary via go:embed,
# so the final image needs nothing but the executable.
RUN CGO_ENABLED=1 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/matam .

# -------------------------------------------------------------- runtime stage
FROM debian:bookworm-slim

# ca-certificates lets the container talk to a reverse proxy over TLS if needed;
# tzdata carries the Asia/Bahrain zone used for closing dates and timestamps.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

ENV TZ=Asia/Bahrain

# Run unprivileged. The data volume is chowned to this user below.
RUN useradd --system --uid 10001 --home /app --shell /usr/sbin/nologin matam

WORKDIR /app
COPY --from=build /out/matam /app/matam

# Database, uploaded candidate photos and the exported workbook all live here.
RUN mkdir -p /data && chown -R matam:matam /data /app
VOLUME ["/data"]

ENV DATA_DIR=/data \
    LISTEN_ADDR=:8080 \
    COOKIE_SECURE=true

EXPOSE 8080
USER matam

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/matam", "-healthcheck"]

ENTRYPOINT ["/app/matam"]
