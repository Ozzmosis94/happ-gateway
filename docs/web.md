# Happ Gateway (MVP)

An opt-in, authenticated web UI around the existing `profile`, `store`, `check`, and embedded `xray` packages. Proxy mode only: **no TUN, host routing, or firewall modifications**.

## Run from the published image (Linux)

Docker Engine is sufficient; no repository checkout or pre-generated secret is needed.

```sh
docker run -d --name happ-gateway --restart unless-stopped --network host -v happ-gateway-data:/data ghcr.io/ozzmosis94/happ-gateway:latest
docker logs happ-gateway
```

The container generates an access token on first start, saves it in `/data/web-token` and prints the token to the Docker logs at startup. Protect both the logs and the persistent volume. Supply `WEB_TOKEN` if you prefer to manage the secret yourself.

Open <http://127.0.0.1:8088> on the Linux host. From another machine use `ssh -L 8088:127.0.0.1:8088 user@server-ip`. The token stays in memory of the browser tab. The volume `happ-gateway-data` holds the state and access token and survives container recreation.

To run via Docker Compose, use `docker compose up -d` from the repository checkout; the Compose file no longer requires an `.env`.

## Proxy access

The default binds SOCKS5 to `127.0.0.1:10808` and HTTP to `127.0.0.1:10809` on the **Linux host**. They are created after selecting a server in the panel, not at container startup. Containers can use these through host networking or explicitly configured host addresses. Other containers do not automatically route through this proxy.

To expose the proxy to trusted LAN clients, pass these environment variables via `docker run -e ...` or a local Compose override:

```dotenv
# Replace with the actual IPv4 address assigned to the Linux host.
PROXY_LISTEN=192.168.1.210
PROXY_ALLOW_LAN=true
# Optional source IP allowlist, using comma-separated networks.
PROXY_ALLOW_CIDRS=192.168.1.0/24,172.18.0.0/16
```

Then recreate the container with the new environment values. The embedded core binds additional SOCKS5/HTTP listeners at the specified private IPv4 address, while retaining the loopback endpoints for health checks. **The SOCKS5/HTTP listeners have no authentication.** Restrict access at the host firewall, check Docker bridge ranges before using the example allowlist, and never forward these ports on the router. `PROXY_LISTEN=0.0.0.0` is intentionally not supported.

The web UI itself stays on 127.0.0.1 by default. If exposing it through a reverse proxy, use HTTPS and additional access controls. The API key travels via `Authorization: Bearer`; do not expose the UI over unencrypted public HTTP.

## API

All `/api/*` endpoints require `Authorization: Bearer <WEB_TOKEN>`.

| Method | Path | Payload |
|---|---|---|
| GET | `/api/status` | Core and health-check status, exit IP |
| GET | `/api/subscriptions` | Sanitized summary, no subscription URLs |
| POST | `/api/subscriptions` | `{"name":"main","url":"https://..."}` |
| GET | `/api/subscriptions/{name}/servers` | Server index, protocol, address, tag; **no credentials** |
| POST | `/api/subscriptions/{name}/activate` | none |
| POST | `/api/subscriptions/{name}/refresh` | `{}` or `{"force":true}` |
| DELETE | `/api/subscriptions/{name}` | none |
| POST | `/api/connect` | `{"subscription":"main","index":1}` |
| POST | `/api/disconnect` | none |

An update that returns zero servers does not overwrite cached servers. A drop of more than 50% returns HTTP 409 until explicitly confirmed with `force`. Deleting the currently connected subscription disconnects it. Changing the active subscription does not implicitly switch the running connection.

## Status semantics and limitations

`running` means the embedded xray instance started, **not** that the remote server is reachable. `health.healthy` and `health.exitIp` are based on an outgoing request via the loopback SOCKS5 proxy to Cloudflare trace, checked every 30 seconds. An unreachable check endpoint can cause a false negative without stopping the proxy. This first iteration does not implement automatic reconnection/failover, subscription refresh scheduling, traffic metrics from the core, TUN mode, or authentication on the proxy ports.
