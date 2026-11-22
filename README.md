# Happ Gateway

[English](README.md) | [Русский](README.ru.md)

> **Unofficial project.** Happ Gateway is an independently developed, self-hosted gateway for Happ-compatible subscriptions. It is **not an official Happ product**, and is not affiliated with, endorsed by, or supported by the Happ developers or publisher. **Happ** is referenced only to describe subscription compatibility.

**Happ Gateway** is a self-hosted proxy/subscription manager, featuring an independent Go CLI, embedded Xray-core, and a password-protected web dashboard. The project is developed from this repository without tracking releases or commits from intermediary forks.

> **Code provenance:** Happ Gateway is based on earlier public happ-cli/proxray code. See [NOTICE.md](NOTICE.md) about provenance and distribution rights. This repository does not assert independent licensing of upstream contributions.

## Features

- Subscription management, including HAPP-compatible headers and base64/JSON formats
- CLI with SOCKS5/HTTP, TUN, and TUN-direct modes
- Web API for subscriptions, server switching, connection status, and exit IP checks
- Local-only administration by default; opt-in private LAN proxy listeners
- One Go binary, `happ-gateway`, used for both CLI and the web service
- Docker Compose deployment on Linux

## Build and test

Requires Go 1.26.4 or later.

```bash
go test ./...
go vet ./...
go build -o happ-gateway ./cmd/happ-gateway
```

## CLI

```bash
happ-gateway sub add https://example.invalid/sub/TOKEN --name home
happ-gateway sub list
happ-gateway list
happ-gateway connect 1
happ-gateway --home ./state server
```

The default per-user data directory is named `happ-gateway`. Existing happ-cli state is **not** automatically moved; transfer a copy of the previous `state.json` and `device.json` to the new directory if needed.

## One-command Docker startup (Linux)

**No repository clone, local build, configuration file, or manual token generation required.**

```bash
docker run -d --name happ-gateway --restart unless-stopped --network host -v happ-gateway-data:/data ghcr.io/ozzmosis94/happ-gateway:latest
```

On the Linux host, open **http://127.0.0.1:8088**. On a new installation, choose an administrator username and password (12 characters minimum). Credentials are stored as an Argon2id hash in `/data/admin.json` and authenticated via a signed HttpOnly cookie.

When upgrading an existing installation, open the dashboard and choose an administrator username and password. No legacy access token is needed. Restrict dashboard access to trusted clients: before initial registration, the first client to reach the setup endpoint can claim the administrator account.

Initial administrator registration is restricted to localhost. For setup from another LAN computer, use an SSH tunnel (`ssh -L 8088:127.0.0.1:8088 user@server-ip`) and open `http://127.0.0.1:8088`. No old access token is needed.

Keep the `/data` volume, which holds subscriptions and authentication state.

The SOCKS5 and HTTP proxies listen locally on ports 10808 and 10809 after you connect to a server in the dashboard. For remote administration, open an SSH tunnel: `ssh -L 8088:127.0.0.1:8088 user@server-ip`.

Update without losing the saved subscriptions and administrator:

```bash
docker pull ghcr.io/ozzmosis94/happ-gateway:latest
docker rm -f happ-gateway
docker run -d --name happ-gateway --restart unless-stopped --network host -v happ-gateway-data:/data ghcr.io/ozzmosis94/happ-gateway:latest
```

Docker Compose is also supported: `docker compose up -d` from the repository directory. Docker host networking requires Linux (or special Docker Desktop configuration). The published image supports linux/amd64 and linux/arm64.

## Automatic connection restore and remembered login

When you select a proxy server in the dashboard, Happ Gateway persists that choice and attempts to reconnect to the **same server** when the container restarts. Server identity is verified by a hash of the original share link rather than a fragile list position. After upgrading from an older image, connect manually **once** to save a selection. Explicitly clicking Disconnect disables autoconnection. If the server was removed from the cached subscription, restore is skipped and logged.

The dashboard remembers login for 30 days using an HttpOnly, SameSite=Strict cookie. Logging out clears it, while changing the session signing key invalidates existing sessions. Local-network HTTP is not encrypted; use HTTPS for untrusted networks.

## Release and dependency policy

- Releases are built from this repository, not from another user's fork.
- Xray-core is embedded as a versioned Go module in `go.mod`.
- Dependency update PRs are opened weekly, but stable releases require tests and manual approval.
- Do not enable publication of third-party-derived binary distributions without confirming distribution rights.

## Security

The web API requires an authenticated administrator session. Do not forward the proxy or dashboard ports from a public router. The proxy endpoints are not password-protected: use localhost or an explicitly approved LAN address, restrict network access, and protect the configuration volume. Subscription links and state files may contain credentials.

## Scope

The web panel currently supports proxy mode, while the CLI continues to support TUN. The feature branch includes periodic health checks, automatic server failover after three failed checks, and bounded retries. Automatic subscription refresh runs every six hours.

## Fast recovery for large subscriptions

If the connection fails, Happ Gateway first probes up to **4 ranked nodes** using a persisted successful-connection history. Probes run in isolated temporary Xray instances without stopping the active proxy. If the quick pass fails, the subscription is **refreshed immediately** (at most once every 5 minutes); a failed refresh preserves cached servers. Remaining candidates are checked in **bounded batches of 8 concurrent probes**, with a 5-second budget per candidate, and the fastest verified egress is selected. Scheduled six-hour refresh is independent of emergency refresh.

