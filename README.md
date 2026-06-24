# daema-coin-server

Go backend for the Daema coin client API contract.

Detailed API specification: [docs/backend-api-spec.md](docs/backend-api-spec.md)

## Run

```bash
docker-compose up -d postgres
mise run dev
```

The server listens on `:8080` by default.

## Docker

```bash
docker build -t daema-coin-server .
docker run --rm -p 8080:8080 \
  -e PORT=8080 \
  -e CORS_ALLOW_ORIGIN=http://localhost:5173 \
  -e PUBLIC_BASE_URL=http://localhost:5173 \
  -e DATABASE_URL='postgres://daema:daema@host.docker.internal:5432/daema_coin?sslmode=disable' \
  -e APP_TIMEZONE=Asia/Seoul \
  -e GITHUB_OAUTH_CLIENT_ID='<github-oauth-client-id>' \
  -e GITHUB_OAUTH_CLIENT_SECRET='<github-oauth-client-secret>' \
  -e GITHUB_OAUTH_REDIRECT_URI='http://localhost:8080/api/auth/github/callback' \
  -e GITHUB_OAUTH_SCOPES='read:user user:email repo' \
  -e GITHUB_APP_INSTALL_URL='<github-app-install-url>' \
  -e GITHUB_WEBHOOK_SECRET='<github-app-webhook-secret>' \
  -e API_FOOTBALL_KEY='<api-football-key>' \
  daema-coin-server
```

The server reads real environment variables first. `.env` is only a local development convenience file loaded when present.

When running the container against the compose Postgres from Docker Desktop, set `DATABASE_URL` to a reachable host address:

```env
DATABASE_URL=postgres://daema:daema@host.docker.internal:5432/daema_coin?sslmode=disable
```

## Storage

All mutable app data is stored in PostgreSQL through `DATABASE_URL`.

Default:

```text
postgres://daema:daema@localhost:5432/daema_coin?sslmode=disable
```

The server starts with an empty database. Empty lists or zero aggregates mean the database has no records for that domain.

## GitHub OAuth

Login uses GitHub OAuth web application flow.

1. Register a GitHub OAuth App.
2. Set the callback URL to `http://localhost:8080/api/auth/github/callback`.
3. Copy `.env.example` values into your local environment and set:
   - `GITHUB_OAUTH_CLIENT_ID`
   - `GITHUB_OAUTH_CLIENT_SECRET`
   - `GITHUB_OAUTH_REDIRECT_URI`
4. Start login:
   - Browser redirect: `GET /api/auth/github/login?role=customer`
   - JSON URL: `GET /api/auth/github/login?role=seller&format=json`
   - SPA exchange: `POST /api/auth/github/exchange`

Seller/admin roles are granted by allowlist:

- `GITHUB_OAUTH_SELLER_LOGINS`
- `GITHUB_OAUTH_SELLER_EMAILS`
- `GITHUB_OAUTH_ADMIN_LOGINS`
- `GITHUB_OAUTH_ADMIN_EMAILS`

For local-only prototyping, `GITHUB_OAUTH_TRUST_REQUESTED_ROLE=true` lets the requested `role` become trusted after GitHub login.

## GitHub Commits

Commit history APIs read commits stored from GitHub App `push` webhooks. GitHub OAuth is still used to identify the signed-in user, but page requests do not scan repositories through GitHub REST.

- `GET /api/customer/github/commits?from=2026-06-01&to=2026-06-24`
- `GET /api/customer/github/commit-activity`
- `GET /api/customer/github/commit-stats?groupBy=month`
- `GET /api/customer/points/commit-activity`
- `GET /api/customer/points/commit-stats`
- `GET /api/customer/points/commit-transactions`

Required GitHub App settings:

- Webhook URL: `http://localhost:8080/api/github/webhooks`
- Webhook events: `push`, `ping`, `installation`, `installation_repositories`
- Repository permission: Contents read-only is enough for push payload delivery
- `GITHUB_WEBHOOK_SECRET` must match the GitHub App webhook secret
- `GITHUB_APP_INSTALL_URL` should point to the GitHub App install page

Relevant GitHub docs:

- [Authorizing OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)
- [Authenticating to the REST API with an OAuth app](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authenticating-to-the-rest-api-with-an-oauth-app)
- [GitHub App webhooks](https://docs.github.com/en/webhooks)

## API-FOOTBALL

World Cup match APIs require API-FOOTBALL. Without `API_FOOTBALL_KEY`, these endpoints return `503 API_FOOTBALL_NOT_CONFIGURED`; upstream failures return `502 API_FOOTBALL_UNAVAILABLE`.

Required header for upstream calls:

```text
x-apisports-key: <API_FOOTBALL_KEY>
```

Default upstream base URL:

```text
https://v3.football.api-sports.io
```

Useful env vars are listed in `.env.example`.

## Endpoint Coverage

Customer:

- `GET /api/customer/home`
- `GET /api/customer/me`
- `GET /api/customer/wallet/balances`
- `POST /api/customer/pay/barcodes`
- `GET /api/customer/booth/home`
- `GET /api/customer/booth/products`
- `GET /api/customer/worldcup/match-days`
- `GET /api/customer/worldcup/matches`
- `GET /api/customer/worldcup/matches/{matchId}`
- `GET /api/customer/worldcup/matches/{matchId}/stats`
- `GET /api/customer/worldcup/matches/{matchId}/lineups`
- `POST /api/customer/worldcup/matches/{matchId}/predictions`

Seller:

- auth, seller profile, booth/staff/profile/status APIs
- product, image, inventory, purchase-limit APIs
- order queue, order status, cancel/refund APIs
- pickup voucher verification/redeem APIs
- POS barcode lookup, payment intent, capture/cancel/refund APIs
- visit verification, booth ranking, inquiries, notices APIs
- dashboard, settlement, sales/inventory reports, export APIs

Admin:

- festival, booth, booth-category, map master-data APIs
- users, import, roles, role-assignment APIs
- wallets, adjustments, ledger, ledger export APIs
- reward rule, notice, promotion, notification, file upload APIs
- World Cup teams, matches, lineups, stats, prediction settlement APIs
- audit logs, system health, jobs, incident APIs

All responses follow the client document's `ApiResponse<T>` / `ApiError` shape.
