# Happ Gateway (MVP)

An opt-in, authenticated web UI around the existing `profile`, `store`, `check`, and embedded `xray` packages. Proxy mode only: **no TUN, host routing, or firewall modifications**.

## Run on a Linux host or Proxmox VM

Requires Docker Engine with Compose, and a host that allows `network_mode: host` (Linux). Use the repository root:

```sh
printf 'WEB_TOKEN=%s\n' "$(openssl rand -hex 24)" > .env
chmod 600 .env
docker compose -f compose.yml up -d --build
```

The web panel listens at `http://127.0.0.1:8088` on the host. Paste the generated `WEB_TOKEN` into its login form. On a separate workstation, open an SSH tunnel:

```sh
ssh -L 8088:127.0.0.1:8088 user@your-server
```

Then open `http://127.0.0.1:8088` in the browser on the workstation. The token remains only in the tab's memory (re-enter it after a reload). No external OAuth or database is needed.

State persists in the `gateway_state` Docker volume (`/data/state.json` and `/data/device.json`). A dedicated volume is recommended so no other running happ process modifies the same state file concurrently. Do not publish backup copies of `state.json`: subscription URLs and server links contain secrets.

## Proxy access

The default binds SOCKS5 to `127.0.0.1:10808` and HTTP to `127.0.0.1:10809` on the **Linux host**. They are created after selecting a server in the panel, not at container startup. Containers can use these through host networking or explicitly configured host addresses. Other containers do not automatically route through this proxy.

To expose the proxy to trusted LAN clients (including containers with a route to the host), set the following in `.env`:

```dotenv
# Replace with the actual IPv4 address assigned to the Linux host.
PROXY_LISTEN=192.168.1.210
PROXY_ALLOW_LAN=true
# Optional source IP allowlist, using comma-separated networks.
PROXY_ALLOW_CIDRS=192.168.1.0/24,172.18.0.0/16
```

Then recreate the service. The embedded core binds additional SOCKS5/HTTP listeners at the specified private IPv4 address, while retaining the loopback endpoints for health checks. **The SOCKS5/HTTP listeners have no authentication.** Restrict access at the host firewall, check Docker bridge ranges before using the example allowlist, and never forward these ports on the router. `PROXY_LISTEN=0.0.0.0` is intentionally not supported.

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
