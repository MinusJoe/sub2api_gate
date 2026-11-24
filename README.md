# Sub2API Gate

AI API gateway based on [Wei-Shaw/Sub2API](https://github.com/Wei-Shaw/sub2api). This repository tracks upstream Sub2API and maintains its own releases.

English · [简体中文](README_CN.md) · [日本語](README_JA.md)

- Upstream source: [Sub2API v0.2.25](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.25), see the [sync record](.github/upstream-sync.json).
- Published images and binaries: [GitHub Releases](https://github.com/MinusJoe/sub2api_gate/releases).

## Docker Compose

For a new installation, run the deployment preparation script in a new directory:

```bash
mkdir -p sub2api-deploy
cd sub2api-deploy
curl -fsSLO https://raw.githubusercontent.com/MinusJoe/sub2api_gate/main/deploy/docker-deploy.sh
bash docker-deploy.sh
docker compose up -d
docker compose logs -f sub2api
```

Open `http://localhost:8080` (or your server address) to continue setup. The published image is `ghcr.io/minusjoe/sub2api_gate`. The Compose service is `sub2api`.

To upgrade an existing installation, use the same Compose files and project directory as before:

```bash
docker compose pull sub2api && docker compose up -d --no-deps sub2api
```

See the [deployment guide](deploy/README.md), [environment example](deploy/.env.example), and [configuration example](deploy/config.example.yaml) for setup and maintenance.

## Build from source

```bash
git clone https://github.com/MinusJoe/sub2api_gate.git
cd sub2api_gate/frontend
pnpm install --frozen-lockfile
pnpm build
cd ../backend
go build -tags embed -o sub2api ./cmd/server
./sub2api
```

Licensed under [GNU LGPL v3.0 or later](LICENSE). Thanks to the upstream Sub2API contributors.
