> **Community Project** — This is an independent, community-maintained Docker logging plugin. It is **not affiliated with, endorsed by, or supported by SolarWinds** in any way. Use at your own risk.

---

# docker-plugin-swo

A Docker logging plugin that ships container logs to [SolarWinds Observability](https://www.solarwinds.com/solarwinds-observability) (SWO) via syslog-over-HTTPS.

## Installation

    docker plugin install ghcr.io/omnimodular/docker-plugin-swo

The plugin requires host network access for sending logs. Accept the permission when prompted.

## Configuration Options

| Option | Required | Description | Default |
|--------|----------|-------------|---------|
| `swo-url` | Yes | SWO HTTPS log ingestion endpoint | — |
| `swo-token` | Yes | SWO API token for authentication | — |
| `tag` | No | Docker log-tag template for the syslog app name | Container name (without leading `/`), then first 12 ID characters |
| `swo-service-name` | No | Value for `service.name` OpenTelemetry resource attribute | — |
| `swo-json-limit` | No | Max items per JSON object/array before truncation (0 to disable) | `20` |

## Usage

### Per-container

    docker run --rm \
        --log-driver ghcr.io/omnimodular/docker-plugin-swo \
        --log-opt swo-url=https://your-swo-endpoint/logs \
        --log-opt swo-token=YOUR_TOKEN \
        --log-opt swo-service-name=my-app \
        ubuntu bash -c 'echo "Hello from SWO"'

### Daemon default

Set the default logging driver in `/etc/docker/daemon.json`:

    {
      "log-driver": "ghcr.io/omnimodular/docker-plugin-swo",
      "log-opts": {
        "swo-url": "https://your-swo-endpoint/logs",
        "swo-token": "YOUR_TOKEN",
        "swo-service-name": "my-app",
        "swo-json-limit": "20"
      }
    }

Then restart Docker:

    sudo systemctl restart docker

### Compose service-and-number naming

The `tag` option uses Docker's [standard log-tag parser](https://github.com/moby/moby/blob/v24.0.7/daemon/logger/loggerutils/log_tag.go) and evaluates against Docker's `logger.Info` once when logging starts. Missing or empty `tag` keeps the existing container-name default. Empty rendered output falls back to the original name, then the first 12 characters of the container ID (or the whole ID if shorter). Invalid templates, execution failures, or names containing whitespace or control characters fail logging startup.

This template combines the Compose [service name and instance number](https://github.com/docker/compose/blob/main/pkg/api/labels.go)—for example, `web-1`. It preserves hyphens in service names. If either label is missing or empty, it uses the full container name:

```gotemplate
{{$service := index .ContainerLabels "com.docker.compose.service"}}{{$number := index .ContainerLabels "com.docker.compose.container-number"}}{{if and $service $number}}{{$service}}-{{$number}}{{else}}{{.Name}}{{end}}
```

For daemon-wide Compose naming, use this valid JSON in `/etc/docker/daemon.json` (replace the endpoint and token):

```json
{
  "log-driver": "ghcr.io/omnimodular/docker-plugin-swo",
  "log-opts": {
    "swo-url": "https://your-swo-endpoint/logs",
    "swo-token": "YOUR_TOKEN",
    "swo-json-limit": "20",
    "tag": "{{$service := index .ContainerLabels \"com.docker.compose.service\"}}{{$number := index .ContainerLabels \"com.docker.compose.container-number\"}}{{if and $service $number}}{{$service}}-{{$number}}{{else}}{{.Name}}{{end}}"
  }
}
```

For Compose, the template above gives each container its own app name, such as `web-1` and `web-2`. Leave `swo-service-name` unset so SolarWinds uses those names; setting it overrides them. The hostname stays unchanged.

Restart Docker and recreate existing application containers to apply the new logging defaults. Check Compose logging overrides as well, since they may override daemon defaults.

## Notes

- `docker logs` is supported via Docker's built-in [dual logging](https://docs.docker.com/engine/logging/dual-logging/) cache (Docker 20.10+).
- Log messages are formatted as RFC 5424 syslog and sent via HTTPS with bearer token auth.
- JSON log messages are automatically minified (large objects/arrays truncated) before shipping.
- Syslog severity is auto-detected from a `"level"` field in JSON messages, or from the presence of `"error"` in plain text.

## Building locally

    ./local-build.sh

View plugin logs:

    journalctl -u docker.service -f

---

## Disclaimer

This project is an independent, community-driven effort and is not affiliated with, sponsored by, endorsed by, or in any way officially connected with SolarWinds Worldwide, LLC, or any of its subsidiaries or affiliates. The names "SolarWinds" and "SWO" as well as related names, marks, emblems, and images are trademarks of their respective owners. Use of these names in this project is for identification purposes only and does not imply any affiliation or endorsement.

This software is provided "as is", without warranty of any kind, express or implied. The maintainers make no guarantees regarding reliability, availability, or fitness for any particular purpose. Use this software at your own risk.
