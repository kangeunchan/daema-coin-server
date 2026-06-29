# Backend Refactor Plan

## Goals

- Keep request behavior and API contracts stable while reducing file size and ownership ambiguity.
- Split backend code by operational responsibility so production incidents can be traced to a small file.
- Preserve the current `internal/server` package until domain boundaries are stronger; avoid a premature package tree that forces wide exported APIs.
- Prefer mechanical moves first, then behavior-changing refactors only with focused tests.

## Current Hotspots

| Area | Current issue | Target shape |
| --- | --- | --- |
| `server.go` | Previously mixed boot, routes, middleware, auth, domain handlers, and clients. | Boot-only file with server construction and lifecycle. |
| `store.go` | DB lifecycle, generic JSONB resources, wallet ledger transactions, payment transactions, sessions, internal accounts, and serialization helpers share one file. | Store files split by persistence concern. |
| `worldcup_handlers.go` | Match read handlers, prediction commands, settlement worker, and ledger read handlers share one file. | Worldcup match, prediction, settlement, and ledger handlers separated. |
| `resources.go` | Generic `map[string]any` helpers centralize too much domain behavior. | Keep as compatibility layer, then migrate high-risk domains to typed services. |
| `http_common.go` | Logging, CORS, CSRF, response encoding, env parsing, and small presentation helpers share one file. | Split when behavior changes are needed; keep stable for now. |

## Refactor Sequence

1. Complete mechanical file split.
   - `server.go` remains boot/lifecycle only.
   - Handlers are grouped by customer, seller, admin, GitHub, worldcup.
   - External clients live outside handler files.
2. Split persistence code.
   - `store.go`: connection, migration, transaction helper, base CRUD, table mapping.
   - `store_customer.go`: customer profile and GitHub user lookup.
   - `store_wallet.go`: wallet balance and ledger mutation.
   - `store_prediction.go`: worldcup prediction transactional writes.
   - `store_payment.go`: payment intent capture, cancel, refund.
   - `store_auth.go`: sessions, OAuth state, internal accounts.
   - `store_util.go`: generic map/decode/id helpers.
3. Split worldcup handlers.
   - `worldcup_handlers.go`: match/team read model and match endpoints.
   - `prediction_handlers.go`: prediction create/cancel/summary and settlement.
   - `ledger_handlers.go`: customer ledger read endpoints.
4. Add behavior tests around any non-mechanical changes.
   - Idempotent payment capture/cancel/refund.
   - Prediction stake/refund/settlement accounting.
   - CSRF, upload auth, request ID, query limits.
5. Gradually replace generic resource maps in high-risk write paths.
   - Start with money-moving paths: wallet ledger, payment, prediction.
   - Introduce typed request/record structs only where they prevent real bugs.

## Rules

- Mechanical moves must pass `go test ./...`, `go test -race ./internal/server`, `go vet ./...`, and `govulncheck ./...`.
- Do not change routes or response JSON shape during file-split commits.
- Keep DB schema changes in migrations, not embedded in handlers.
- Avoid exporting symbols only to satisfy package layout; keep `package server` until a domain package has a clean interface.

## This Pass

- Keep the already completed `server.go` split.
- Split `store.go` by persistence concern.
- Split `worldcup_handlers.go` into worldcup, prediction, and ledger files.
- Run the full backend verification suite after the split.

## Remaining Refactor Backlog

### P0: Type Safety For Money-Moving Paths - Done

The highest-risk code still relies on `map[string]any` for request bodies, stored resources, and ledger/payment payloads. This is acceptable as a compatibility layer, but not as the long-term shape for money-moving flows.

Target first:

- Payment intent capture, cancel, and refund in `store_payment.go`.
- Wallet ledger creation and wallet balance adjustment in `store_wallet.go`.
- Worldcup prediction stake, cancel, and settlement in `prediction_handlers.go` and `store_prediction.go`.

Expected shape:

- Introduce typed request structs for handler input.
- Introduce typed record structs for payment, ledger, wallet balance, and prediction data.
- Keep JSON response compatibility by mapping typed records back to the current response shape.
- Add focused tests for idempotency conflicts, insufficient balance, partial refund, duplicate settlement, and cancellation.

Current progress:

- Added typed payment capture/cancel/refund requests and payment intent/payment records.
- Added typed prediction create/stake/cancel/settlement records.
- Added typed wallet ledger requests used by payment, prediction, admin wallet adjustment, signup bonus, and GitHub reward flows.
- Kept JSONB compatibility by mapping typed fields back to the existing response payloads.
- Added unit and integration coverage for parsing, idempotency, insufficient balance, refund limits, and settlement behavior.

### P1: Route And Authorization Policy Structure - Done

`routes.go` now centralizes route registration, but authorization policy is still inferred from URL prefixes inside `authzMiddleware`. That makes accidental policy gaps harder to spot during review.

Target shape:

- Add route group helpers for `public`, `customer`, `seller`, `admin`, and `adminOrBooth`.
- Register routes through those helpers instead of relying only on path-prefix checks.
- Keep the existing middleware as a fallback guard until route-level policy is complete.
- Add tests proving representative routes require the intended role.

### P1: Handler And Service Separation - Done

Handlers still mix HTTP parsing, validation, business rules, store calls, and response formatting. This makes business logic hard to test without HTTP setup.

Target first:

- Payment service for barcode lookup, intent creation, capture, cancel, and refund.
- Prediction service for create, cancel, settlement, and worker cycle.
- Admin account service for account create/update/reset-password.

Expected shape:

- Handlers parse HTTP and translate service errors to API errors.
- Services own business rules and call stores.
- Stores remain persistence-only.
- Unit tests cover services without `httptest` where possible.

### P1: Validation Layer - Done

Most handlers still parse request bodies through `requestPayload` and then manually pull values from `map[string]any`.

Target shape:

- Add small decode helpers for typed JSON bodies.
- Validate required fields, enum values, amount ranges, and ownership identifiers at the boundary.
- Return stable API error codes from validation failures.
- Avoid accepting unknown fields for high-risk mutation endpoints.

Current progress:

- Added strict typed JSON decoding for fixed-schema admin account mutation endpoints.
- Added account create/update/reset-password request types that preserve existing `loginId`, `username`, and `login` create aliases.
- Added regression coverage proving strict decode rejects unknown fields.
- Added service-level validation for customer cart/order mutation required fields and positive quantity.
- Added stable API error mapping for cart/order validation failures.
- Kept money-moving payment and prediction mutation validation in typed request/record helpers to preserve existing JSONB response compatibility.

### P2: HTTP Common Split - Done

`http_common.go` is still broad. It is acceptable for now, but should be split once behavior work touches those areas.

Target files:

- `response.go`: API response envelope and JSON writer.
- `logging.go`: request logging, header/query redaction, request IDs.
- `security.go`: CORS, CSRF, origin checks.
- `env.go`: environment parsing helpers.
- `presentation.go`: small response formatting helpers such as `amount`, `media`, and date/number formatting.

### P2: GitHub Domain Split - Done

`github_handlers.go` previously contained customer-facing commit endpoints, webhook handling, reward logic, and commit aggregation helpers.

Target files:

- `github_handlers.go`: HTTP endpoints only.
- `github_webhook.go`: signature verification and webhook storage.
- `github_rewards.go`: reward eligibility and daily reward counting.
- `github_commits.go`: commit aggregation, grouping, relative labels.

### P2: Store Interface And Testability - Done

Current tests either use HTTP handlers or integration tests against a real database when `DATABASE_URL` is available. More service tests would be easier with narrow store interfaces.

Target shape:

- Define small interfaces per service, not one large repository interface.
- Use in-memory fakes for payment and prediction service tests.
- Keep integration tests for SQL transaction behavior and migration coverage.

Current progress:

- Added narrow `paymentServiceStore`, `predictionStore`, `predictionMatchReader`, `predictionWalletReader`, and `adminAccountStore` interfaces.
- Kept `postgresStore` as the production implementation while allowing service tests to use in-memory fakes.
- Added service tests for payment intent barcode/idempotency logic, prediction stake creation, and admin account role-specific validation.

### P3: Generic Resource Compatibility Layer - Done

The generic JSONB resource layer is useful for breadth, but it hides domain invariants. It should become a compatibility layer rather than the core model for important writes.

Migration order:

1. Keep read-heavy, low-risk endpoints on generic resources.
2. Move mutation-heavy admin/seller endpoints to typed commands where validation matters.
3. Replace generic `createResourceHandler` and `updateResourceHandler` usage endpoint by endpoint.
4. Keep response shape stable while strengthening internal models.

Current progress:

- Removed route-level `createResourceHandler` and `updateResourceHandler` registrations.
- Replaced seller/customer/admin inline generic route handlers with named handlers.
- Removed the old HTTP-coupled `createResource`, `updateResource`, and `putResource` helpers.
- Added `resourceCommandService` as the remaining compatibility layer with explicit create/patch/put command structs.
- Moved seller mutation endpoints behind `sellerService` methods.
- Moved customer mutation endpoints behind `customerService` methods.
- Moved admin resource mutation endpoints behind `adminResourceService` methods.
- Kept response JSON and resource ID compatibility while removing direct generic helper calls from domain handlers.

## Recommended Next Pass

1. Convert payment capture/cancel/refund to typed request and record helpers. Done.
2. Add focused tests for payment request/record parsing and keep integration idempotency coverage. Done.
3. Introduce route group helpers for authorization policy clarity. Done.
4. Convert prediction create/cancel/settlement to typed helpers. Done.
5. Split `http_common.go` after the security/response boundaries are touched by tests. Done.
6. Extract payment, prediction, and admin account services behind narrow store interfaces. Done.
7. Add strict typed JSON decoding for fixed-schema admin account mutation endpoints. Done.
8. Move seller/customer/admin mutation endpoints off HTTP-coupled generic resource helpers. Done.
9. Add customer cart/order validation for required purchase fields and quantity ranges. Done.

## Current Progress

- Added typed payment request helpers for capture, cancel, and refund.
- Added typed payment intent and payment record readers for normalized currency, amount, customer, booth, order, and status fields.
- Updated payment store methods to accept typed requests instead of raw request maps.
- Preserved existing response and JSONB storage shape by retaining request extras and overwriting canonical fields.
- Added unit tests for payment typed request parsing and payment record validation.
- Kept existing integration coverage for payment capture/refund idempotency and insufficient-balance rollback.
- Added route registration helpers for `public`, `customer`, `seller`, `admin`, and `adminOrBooth`.
- Added route policy tests for role enforcement and seller booth scope enforcement.
- Added typed prediction create, stake, cancel, and settlement record helpers.
- Updated prediction handlers and store methods to use typed stake/cancel requests.
- Preserved existing prediction JSONB storage and response shape by retaining request extras and overwriting canonical fields.
- Added unit tests for prediction request parsing, record validation, and invalid-row settlement filtering.
- Split `http_common.go` into context keys, logging, request ID, response, security, env, and presentation files.
- Split `github_handlers.go` into handler, webhook, reward, and commit aggregation files.
- Removed route-level generic resource handler registrations and replaced them with named endpoint handlers.
- Extracted payment intent/barcode/capture/cancel/refund orchestration into a payment service.
- Extracted worldcup prediction create/cancel/settlement orchestration into a prediction service.
- Extracted admin account create/update/reset-password orchestration into an admin account service.
- Added narrow service store interfaces and in-memory service tests for payment, prediction, and admin account behavior.
- Added strict typed JSON decoding for admin account create/update/reset-password requests.
- Added `resourceCommandService` with explicit create/patch/put command structs as the JSONB compatibility layer.
- Moved seller, customer, and admin mutation handlers to domain service methods.
- Removed unused HTTP-coupled generic resource handler/helper functions.
- Added customer cart/order validation tests that prove invalid purchase mutations are rejected before store writes.

## Verification

- `go test ./...`: passed.
- `go test -race ./internal/server`: passed.
- `go vet ./...`: passed.
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`: passed, no vulnerabilities found.
