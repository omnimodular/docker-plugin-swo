# docker-plugin-swo

A Docker logging plugin that ships container logs to [SolarWinds Observability](https://www.solarwinds.com/solarwinds-observability) (SWO) via syslog-over-HTTPS.

## Installation

    docker plugin install docker-plugin-swo

The plugin requires host network access for sending logs. Accept the permission when prompted.

## Configuration Options

| Option | Required | Description | Default |
|--------|----------|-------------|---------|
| `swo-url` | Yes | SWO HTTPS log ingestion endpoint | — |
| `swo-token` | Yes | SWO API token for authentication | — |
| `swo-service-name` | No | Value for `service.name` OpenTelemetry resource attribute | — |
| `swo-json-limit` | No | Max items per JSON object/array before truncation (0 to disable) | `20` |

## Usage

### Per-container

    docker run --rm \
        --log-driver docker-plugin-swo \
        --log-opt swo-url=https://your-swo-endpoint/logs \
        --log-opt swo-token=YOUR_TOKEN \
        --log-opt swo-service-name=my-app \
        ubuntu bash -c 'echo "Hello from SWO"'

### Daemon default

Set the default logging driver in `/etc/docker/daemon.json`:

    {
      "log-driver": "docker-plugin-swo",
      "log-opts": {
        "swo-url": "https://your-swo-endpoint/logs",
        "swo-token": "YOUR_TOKEN",
        "swo-service-name": "my-app",
        "swo-json-limit": "20"
      }
    }

Then restart Docker:

    sudo systemctl restart docker

## Notes

- `docker logs` is **not supported** — this plugin ships logs to SWO only.
- Log messages are formatted as RFC 5424 syslog and sent via HTTPS with bearer token auth.
- JSON log messages are automatically minified (large objects/arrays truncated) before shipping.
- Syslog severity is auto-detected from a `"level"` field in JSON messages, or from the presence of `"error"` in plain text.

## Building locally

    ./local-build.sh

View plugin logs:

    journalctl -u docker.service -f
