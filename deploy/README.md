# Deploying whatsapp-go

## Configuration precedence

Highest wins:

1. **Command-line flags** (`--host`, `--port`, `--api-key`, `--db`)
2. **Environment variables** (`WA_*`)
3. **Config file** (`~/.config/wa/config.yaml`, or `--config`)
4. **Built-in defaults** — which differ in container mode (see below)

Loading configuration never writes to disk. If no config file exists, defaults
plus environment variables are used. To persist the effective configuration
(for example a generated API key), run `wa serve --write-config` once on a
writable filesystem.

| Variable | Meaning | Default |
| --- | --- | --- |
| `WA_API_KEY` | Bearer token clients must present | generated, ephemeral |
| `WA_HOST` | Listen address | `localhost`, `0.0.0.0` in a container |
| `WA_PORT` | Listen port | `8080` |
| `WA_DB_PATH` | SQLite database path | `~/.config/wa/wa.db`, `/data/wa.db` in a container |
| `WA_MAX_UPLOAD_SIZE` | Max request body, bytes | `104857600` (100 MiB) |
| `WA_EVENTS_MAX_BUFFER` | Events retained before pruning | `10000` |
| `WA_ALLOW_PRIVATE_WEBHOOK_TARGETS` | Allow webhooks to private/loopback addresses | `false` |
| `WA_CONTAINER` | Force container mode on/off | auto-detected |
| `WA_LOG_FORMAT` | `json` or `text` | `json` in a container, else `text` |
| `WA_LOG_LEVEL` | `debug`/`info`/`warn`/`error` | `info` |

Malformed numeric or boolean values are a startup error rather than a silent
fallback.

## Container mode

Auto-detected from `KUBERNETES_SERVICE_HOST`, `/.dockerenv` or `/proc/1/cgroup`,
and forceable with `WA_CONTAINER`. In container mode:

- the default listen host becomes `0.0.0.0` (bound to `localhost` nothing
  outside the pod's network namespace, including the kubelet's probes, can
  reach the server);
- the default database path moves to `/data`;
- the single-instance PID file is skipped. PIDs restart at 1 in a new
  namespace, so a PID left on the volume by a hard kill will often match an
  unrelated live process and the server would refuse to start forever.
  `--no-pidfile` forces the same behaviour outside a container.

## Endpoints for orchestrators

| Path | Purpose | Auth |
| --- | --- | --- |
| `/api/v1/healthz` | Liveness — 200 while the process serves HTTP | none |
| `/api/v1/readyz` | Readiness — 200 only when WhatsApp is connected, else 503 with the state | none |
| `/api/v1/health` | Legacy alias, behaves like readiness | none |
| `/metrics` | Prometheus text format, dependency-free | none |

A lost WhatsApp session fails readiness but never liveness: restarting the pod
cannot re-pair a device (that needs a human scanning a QR code), so a
restart-on-logout policy would produce a crash loop instead of a fix.

## Applying

```sh
kubectl create secret generic whatsapp-go \
  --from-literal=api-key="wa_$(openssl rand -hex 24)"
kubectl apply -k deploy/
```

Then pair the device once:

```sh
kubectl port-forward svc/whatsapp-go 8080:8080
curl -H "Authorization: Bearer $WA_API_KEY" -XPOST localhost:8080/api/v1/auth/login
```

The response contains a base64 PNG QR code; scan it with the phone. The
session lives on the PVC and survives restarts.

## Why one replica and `strategy: Recreate`

The pod owns a SQLite database and a whatsmeow device session, both
single-writer. A rolling update would briefly run two pods: with a
ReadWriteOnce volume the new pod cannot schedule, and if it could, the two
processes would corrupt the database and fight over the WhatsApp session.
`Recreate` trades a few seconds of downtime for a guaranteed single writer.

## Why `fsGroup: 65532`

The image runs as uid/gid 65532 (`distroless:nonroot`). A PersistentVolume is
attached root-owned; the kubelet only chowns it to the pod's `fsGroup`. Set to
anything else — or omitted — the process cannot open its database under
`/data` and crash-loops on startup.
