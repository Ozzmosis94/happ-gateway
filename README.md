# Raygate

> **Unofficial project.** Raygate is an independently developed, self-hosted gateway for Happ-compatible subscriptions. It is **not an official Happ product**, and is not affiliated with, endorsed by, or supported by the Happ developers or publisher. **Happ** is referenced only to describe subscription compatibility.

**Raygate** is a self-hosted proxy/subscription manager, featuring an independent Go CLI, embedded Xray-core, and a token-protected web dashboard. The project is developed from this repository without tracking releases or commits from intermediary forks.

> **Code provenance:** Raygate is based on earlier public happ-cli/proxray code. See [NOTICE.md](NOTICE.md) about provenance and distribution rights. This repository does not assert independent licensing of upstream contributions.

## Features

- Subscription management, including HAPP-compatible headers and base64/JSON formats
- CLI with SOCKS5/HTTP, TUN, and TUN-direct modes
- Web API for subscriptions, server switching, connection status, and exit IP checks
- Local-only administration by default; opt-in private LAN proxy listeners
- One Go binary, `raygate`, used for both CLI and the web service
- Docker Compose deployment on Linux

## Build and test

Requires Go 1.26.4 or later.

```bash
go test ./...
go vet ./...
go build -o raygate ./cmd/raygate
```

## CLI

```bash
raygate sub add https://example.invalid/sub/TOKEN --name home
raygate sub list
raygate list
raygate connect 1
raygate --home ./state server
```

The default per-user data directory is named `raygate`. Existing happ-cli state is **not** automatically moved; transfer a copy of the previous `state.json` and `device.json` to the new directory if needed.

## Self-hosted web dashboard

```bash
printf 'RAYGATE_WEB_TOKEN=%s\n' "$(openssl rand -hex 24)" > .env
chmod 600 .env
docker compose up -d --build
```

The web dashboard listens at `127.0.0.1:8088` on the Linux host by default.

To open it from a workstation:

```bash
ssh -L 8088:127.0.0.1:8088 user@server-ip
```

Then open <http://127.0.0.1:8088> in your local browser and paste the token from `.env`. The panel manages SOCKS5 on port 10808 and HTTP proxy on 10809; these proxy ports become active after selecting a server. See [docs/web.md](docs/web.md).

## Release and dependency policy

- Releases are built from this repository, not from another user's fork.
- Xray-core is embedded as a versioned Go module in `go.mod`.
- Dependency update PRs are opened weekly, but stable releases require tests and manual approval.
- Do not enable publication of third-party-derived binary distributions without confirming distribution rights.

## Security

The Web API requires a bearer token. Do not forward the proxy or dashboard ports from a public router. The proxy endpoints are not password-protected: use localhost or an explicitly approved LAN address, restrict network access, and protect the configuration volume. Subscription links and state files may contain credentials.

## Scope

The web panel currently supports proxy mode, while the CLI continues to support TUN. The initial iteration does not guarantee persistent connection restoration or automatic server failover.
