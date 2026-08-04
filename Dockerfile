# syntax=docker/dockerfile:1

# ---- build stage ---------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first: this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
# CGO_ENABLED=0 gives a fully static binary that runs on a distroless/static
# base with no libc. It is possible here only because the SQLite driver is
# modernc.org/sqlite (pure Go) rather than mattn/go-sqlite3.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/wa ./cmd/wa

# The runtime image has no shell, so the state directory cannot be created
# there. Create it here with the right ownership and copy it across.
RUN mkdir -p /state && chown 65532:65532 /state

# ---- runtime stage -------------------------------------------------------
# distroless/static: no shell, no package manager, no libc — the smallest
# usable attack surface for a static Go binary. The :nonroot tag ships uid
# and gid 65532, which is what the Deployment's fsGroup must match for the
# persistent volume to be writable.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/wa /usr/local/bin/wa
COPY --from=build --chown=65532:65532 /state /data

# Container-mode defaults. WA_CONTAINER makes detection explicit rather than
# relying on the /proc heuristics, which vary by runtime.
ENV WA_CONTAINER=true \
    WA_HOST=0.0.0.0 \
    WA_PORT=8080 \
    WA_DB_PATH=/data/wa.db \
    WA_LOG_FORMAT=json

# /data holds the WhatsApp session and the message/event database. Losing it
# means re-pairing the phone by hand, so it must be a persistent volume.
VOLUME ["/data"]

EXPOSE 8080

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/wa"]
CMD ["serve"]
