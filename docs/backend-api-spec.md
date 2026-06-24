# Daema Coin Server API Specification

작성일: 2026-06-24  
대상 서버: `daema-coin-server`  
기본 URL: `http://localhost:8080`

## 1. 실행 구성

### 1.1 런타임

- Language: Go `1.26.1`
- Runtime manager: mise
- Database: PostgreSQL 16
- External auth: GitHub OAuth
- External football data: API-FOOTBALL

### 1.2 로컬 실행

```bash
docker-compose up -d postgres
mise run dev
```

서버는 기본적으로 `:8080`에서 실행된다.

### 1.3 PostgreSQL

Docker Compose 서비스:

| 항목 | 값 |
| --- | --- |
| service | `postgres` |
| image | `postgres:16-alpine` |
| container | `daema-coin-postgres` |
| database | `daema_coin` |
| user | `daema` |
| port | `5432` |

PostgreSQL 기본 DSN:

```env
DATABASE_URL=postgres://daema:daema@localhost:5432/daema_coin?sslmode=disable
```

### 1.4 저장 방식

현재 서버는 PostgreSQL `records` 테이블에 도메인별 JSONB 레코드를 저장한다.

```sql
CREATE TABLE IF NOT EXISTS records (
  domain TEXT NOT NULL,
  id TEXT NOT NULL,
  data JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (domain, id)
);
```

서버는 샘플 데이터를 자동 주입하지 않는다. 빈 배열, `null`, 0 집계는 DB에 해당 레코드가 없다는 의미다.

## 2. 환경 변수

### 2.1 서버

| Key | Required | Default / Example | 설명 |
| --- | --- | --- | --- |
| `PORT` | N | `8080` | API 서버 포트 |
| `CORS_ALLOW_ORIGIN` | N | `http://localhost:5173` | 고객 프론트 origin |
| `PUBLIC_BASE_URL` | N | `http://localhost:5173` | 고객 프론트 기본 URL |
| `DATABASE_URL` | Y | `postgres://daema:daema@localhost:5432/daema_coin?sslmode=disable` | PostgreSQL DSN |
| `APP_TIMEZONE` | N | `Asia/Seoul` | 날짜 그룹/오늘 판단 기준 timezone |
| `SESSION_COOKIE_SAMESITE` | N | `lax` | 다른 origin 프론트에서 prod API를 호출하면 `none` 필요 |
| `SESSION_COOKIE_SECURE` | N | HTTPS면 `true` 자동 | `SESSION_COOKIE_SAMESITE=none`이면 반드시 `true` 필요 |
| `SESSION_COOKIE_DOMAIN` | N | empty | 세션 쿠키 domain override |
| `GITHUB_OAUTH_CLIENT_ID` | Y | GitHub OAuth App client id | GitHub OAuth client id |
| `GITHUB_OAUTH_CLIENT_SECRET` | Y | GitHub OAuth App secret | GitHub OAuth client secret |
| `GITHUB_OAUTH_REDIRECT_URI` | Y | `http://localhost:8080/api/auth/github/callback` | GitHub callback URL |
| `GITHUB_OAUTH_SCOPES` | Y | `read:user user:email repo` | private repo commit 조회 포함 |
| `GITHUB_COMMIT_MAX_PAGES` | N | `5` | GitHub commits pagination 제한 |
| `GITHUB_REPOSITORY_MAX_PAGES` | N | `10` | 접근 가능 저장소 탐색 pagination 제한 |
| `GITHUB_APP_INSTALL_URL` | Y | GitHub App install URL | 사용자가 GitHub App을 설치할 URL |
| `GITHUB_APP_INSTALL_ON_LOGIN` | N | `true` | 고객 GitHub 로그인 후 미설치 상태면 GitHub App 설치 화면으로 redirect |
| `GITHUB_WEBHOOK_SECRET` | Y | GitHub App webhook secret | webhook 서명 검증 secret |
| `AUTH_SUCCESS_REDIRECT_URL` | N | `http://localhost:5173/login` | 고객 OAuth 성공 redirect |
| `SELLER_AUTH_SUCCESS_REDIRECT_URL` | N | `http://localhost:5174/` | 셀러 OAuth 성공 redirect |
| `GITHUB_OAUTH_SELLER_LOGINS` | N | comma-separated logins | 셀러 권한 allowlist |
| `GITHUB_OAUTH_SELLER_EMAILS` | N | comma-separated emails | 셀러 권한 allowlist |
| `GITHUB_OAUTH_ADMIN_LOGINS` | N | comma-separated logins | 관리자 권한 allowlist |
| `GITHUB_OAUTH_ADMIN_EMAILS` | N | comma-separated emails | 관리자 권한 allowlist |
| `GITHUB_OAUTH_TRUST_REQUESTED_ROLE` | N | `false` | 로컬 개발용 role trust |
| `API_FOOTBALL_KEY` | Y | API-FOOTBALL key | 경기 데이터 조회 key |
| `API_FOOTBALL_BASE_URL` | N | `https://v3.football.api-sports.io` | API-FOOTBALL base URL |
| `API_FOOTBALL_TIMEZONE` | N | `Asia/Seoul` | 경기 시간대 |
| `API_FOOTBALL_WORLDCUP_LEAGUE` | N | `1` | 월드컵 league id |
| `API_FOOTBALL_WORLDCUP_SEASON` | N | `2026` | 월드컵 시즌 |
| `API_FOOTBALL_WORLDCUP_FROM` | N | `2026-06-11` | 경기 조회 시작일 |
| `API_FOOTBALL_WORLDCUP_TO` | N | `2026-07-19` | 경기 조회 종료일 |

### 2.2 고객 프론트

`apps/customer/.env`

```env
VITE_CUSTOMER_API_BASE_URL=http://localhost:8080/api
```

## 3. GitHub OAuth

### 3.1 GitHub OAuth App 설정

GitHub OAuth App callback URL:

```text
http://localhost:8080/api/auth/github/callback
```

### 3.2 고객 로그인 흐름

1. 고객 프론트 `/login`에서 GitHub 로그인 버튼 클릭
2. 프론트가 `GET /api/auth/github/login?role=customer&redirectAfter=http%3A%2F%2Flocalhost%3A5173%2Flogin`으로 이동
3. 서버가 GitHub authorize URL로 redirect
4. GitHub가 `GET /api/auth/github/callback?code=...&state=...` 호출
5. 서버가 GitHub token 교환, 사용자 조회, 세션 쿠키 저장
6. 서버가 `http://localhost:5173/login?login=success&role=customer`로 redirect
7. 프론트가 `POST /api/auth/github/session` 호출
8. 학생 프로필이 없으면 `profile_required`, 있으면 `authenticated`
9. 학생 정보 입력 후 `PUT /api/auth/me/student-profile`

### 3.3 셀러 로그인 흐름

1. `POST /api/auth/seller/login`
2. 응답의 `authorizeUrl`로 이동
3. OAuth 성공 후 `SELLER_AUTH_SUCCESS_REDIRECT_URL`로 redirect
4. 셀러 권한은 GitHub login/email allowlist 또는 `GITHUB_OAUTH_TRUST_REQUESTED_ROLE=true`로 부여

## 4. 공통 응답

### 4.1 성공

```json
{
  "data": {},
  "meta": {
    "requestId": "string",
    "serverTime": "2026-06-24T00:00:00Z",
    "pagination": {
      "cursor": "",
      "nextCursor": "",
      "limit": 100,
      "hasMore": false
    }
  }
}
```

`pagination`은 목록 응답에만 포함된다.

### 4.2 실패

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "오류 메시지",
    "details": {}
  },
  "meta": {
    "requestId": "string",
    "serverTime": "2026-06-24T00:00:00Z"
  }
}
```

### 4.3 주요 에러 코드

| Code | HTTP | 설명 |
| --- | --- | --- |
| `UNAUTHORIZED` | 401 | 로그인 필요 |
| `SELLER_ROLE_REQUIRED` | 403 | 셀러 권한 필요 |
| `GITHUB_OAUTH_NOT_CONFIGURED` | 503 | GitHub OAuth env 누락 |
| `GITHUB_OAUTH_FAILED` | 400 | OAuth 처리 실패 |
| `INVALID_GITHUB_WEBHOOK_SIGNATURE` | 401 | GitHub webhook 서명 검증 실패 |
| `GITHUB_WEBHOOK_STORE_FAILED` | 500 | GitHub webhook 저장 실패 |
| `API_FOOTBALL_NOT_CONFIGURED` | 503 | API-FOOTBALL key 누락 |
| `API_FOOTBALL_UNAVAILABLE` | 502 | API-FOOTBALL upstream 실패 |
| `DATABASE_READ_FAILED` | 500 | DB 읽기 실패 |
| `DATABASE_WRITE_FAILED` | 500 | DB 쓰기 실패 |
| `RECORD_NOT_FOUND` | 404 | 레코드 없음 |

## 5. 인증/공통 API

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/healthz` | 서버 상태 |
| `GET` | `/api/auth/github/login` | GitHub OAuth 시작 |
| `GET` | `/api/auth/github/callback` | GitHub OAuth callback |
| `POST` | `/api/auth/github/exchange` | SPA code exchange |
| `POST` | `/api/auth/github/session` | 고객 프론트 세션 확인 |
| `GET` | `/api/auth/me` | 현재 세션 조회 |
| `PUT` | `/api/auth/me/student-profile` | 고객 학생 프로필 저장 |
| `POST` | `/api/auth/logout` | 로그아웃 |
| `POST` | `/api/files/uploads` | 파일 업로드 메타 저장 |
| `GET` | `/api/search/suggestions` | 검색 제안 목록 |
| `GET` | `/api/search` | 통합 검색 |

신규 사용자가 학생 프로필을 처음 저장하면 대마코인(`DMC`) 40,000개와 대마포인트(`POINT`) 10,000개를 지급하고 `wallet_balances`, `ledger_transactions`에 기록한다.

## 6. 고객 API

### 6.1 사용자/홈

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/api/customer/me` | 고객 프로필 |
| `GET` | `/api/customer/navigation` | 하단/전체 메뉴 |
| `GET` | `/api/customer/notifications/summary` | 알림 요약 |
| `GET` | `/api/customer/cart/summary` | 장바구니 요약 |
| `GET` | `/api/customer/home` | 홈 통합 데이터 |
| `GET` | `/api/customer/notices/highlight` | 대표 공지 |
| `GET` | `/api/customer/wallet/balances` | 지갑 잔액 |
| `GET` | `/api/customer/benefits/interest` | 이자 혜택 |
| `POST` | `/api/customer/benefits/{benefitId}/claim` | 혜택 claim |
| `GET` | `/api/customer/home/shortcuts` | 홈 단축 메뉴 |
| `GET` | `/api/customer/promotions` | 프로모션 목록 |
| `GET` | `/api/customer/rankings` | 고객/부스 랭킹 |
| `GET` | `/api/customer/festival/banner` | 축제 배너 |
| `GET` | `/api/customer/schedules/highlight` | 축제 일정 하이라이트 |

### 6.2 결제/부스/주문

| Method | Path | 설명 |
| --- | --- | --- |
| `POST` | `/api/customer/pay/barcodes` | 결제 바코드 생성 |
| `GET` | `/api/customer/booth/categories` | 부스 카테고리 |
| `GET` | `/api/customer/booth/banners` | 부스 배너 |
| `GET` | `/api/customer/booth/home` | 부스 홈 통합 |
| `GET` | `/api/customer/booth/products` | 상품 목록 |
| `GET` | `/api/customer/booth/products/search` | 상품 검색 |
| `GET` | `/api/customer/booth/products/{productId}` | 상품 상세 |
| `POST` | `/api/customer/booth/products/{productId}/view` | 상품 조회 기록 |
| `GET` | `/api/customer/booths/{boothId}` | 부스 상세 |
| `POST` | `/api/customer/booths/{boothId}/check-in` | 부스 체크인 |
| `GET` | `/api/customer/booth-rankings` | 부스 랭킹 |
| `POST` | `/api/customer/analytics/impressions` | 노출 분석 저장 |
| `GET` | `/api/customer/cart` | 장바구니 목록 |
| `POST` | `/api/customer/cart/items` | 장바구니 추가 |
| `POST` | `/api/customer/orders/preview` | 주문 미리보기 |
| `POST` | `/api/customer/orders` | 주문 생성 |
| `GET` | `/api/customer/orders/{orderId}` | 주문 상세 |
| `POST` | `/api/customer/favorites` | 즐겨찾기 생성 |
| `DELETE` | `/api/customer/favorites/{targetId}` | 즐겨찾기 삭제 |
| `GET` | `/api/customer/inquiries` | 문의 목록 |
| `POST` | `/api/customer/inquiries` | 문의 생성 |
| `POST` | `/api/customer/shares` | 공유 기록 |

### 6.3 GitHub 커밋 포인트

커밋 API는 GitHub OAuth 세션으로 로그인 사용자를 식별하고, GitHub App `push` webhook으로 DB에 저장된 커밋만 조회한다. 화면 요청 중 GitHub REST로 저장소 전체를 스캔하지 않는다.

커밋 리워드는 GitHub App `push` webhook에서 신규 커밋이 처음 저장될 때 지급한다. 커밋 1개당 대마포인트(`POINT`) 500P를 지급하고, 사용자별 하루 최대 10개 커밋까지만 포인트를 지급한다. 하루 지급 한도를 초과한 커밋은 커밋 내역에는 저장하지만 `rewardedPoints`는 0으로 기록한다.

GitHub App webhook 설정:

| 항목 | 값 |
| --- | --- |
| Webhook URL | `http://localhost:8080/api/github/webhooks` |
| Setup URL | `http://localhost:8080/api/github/app/setup` |
| Events | `push`, `ping`, `installation`, `installation_repositories` |
| Secret | `GITHUB_WEBHOOK_SECRET` |
| Install URL | `GITHUB_APP_INSTALL_URL` |

고객 GitHub OAuth 로그인 후 `GITHUB_APP_INSTALL_ON_LOGIN=true`이고 GitHub App 설치 기록이 없으면 서버가 바로 `GITHUB_APP_INSTALL_URL`로 redirect한다. GitHub App 설치가 끝나면 Setup URL(`/api/github/app/setup`)에서 현재 세션과 `installation_id`를 연결한 뒤 `AUTH_SUCCESS_REDIRECT_URL`로 되돌린다. 이 흐름에서는 프론트에 별도 설치 필요/완료 화면을 만들지 않는다.

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/api/github/app/setup` | GitHub App 설치 후 세션 연결 및 프론트 복귀 |
| `POST` | `/api/github/webhooks` | GitHub App webhook 수신 |
| `GET` | `/api/customer/github/app-installation` | GitHub App 설치 URL |
| `GET` | `/api/customer/github/commits` | GitHub 커밋 목록 |
| `GET` | `/api/customer/github/commit-activity` | 일별 커밋 활동 |
| `GET` | `/api/customer/github/commit-stats` | 월/주/일 커밋 통계 |
| `GET` | `/api/customer/points/commit-activity` | 포인트 화면용 커밋 활동 |
| `GET` | `/api/customer/points/commit-stats` | 포인트 화면용 커밋 통계 |
| `GET` | `/api/customer/points/commit-transactions` | 커밋 보상 거래 |

Query:

| Query | 설명 |
| --- | --- |
| `from` | `YYYY-MM-DD` |
| `to` | `YYYY-MM-DD` |
| `limit` | 목록 제한 |
| `groupBy` | `month`, `week`, `day` |

### 6.4 월드컵

경기/스탯/라인업은 API-FOOTBALL에서 가져온다. 서버는 임의 좌표나 포지션을 계산하지 않는다.

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/api/customer/worldcup/match-days` | 경기일 그룹 |
| `GET` | `/api/customer/worldcup/matches` | 경기 목록 |
| `GET` | `/api/customer/worldcup/matches/{matchId}` | 경기 상세 |
| `GET` | `/api/customer/worldcup/matches/{matchId}/stats` | 경기 지표 |
| `GET` | `/api/customer/worldcup/matches/{matchId}/lineups` | 라인업 |
| `GET` | `/api/customer/worldcup/matches/{matchId}/predictions/summary` | 승부예측 집계 |
| `POST` | `/api/customer/worldcup/matches/{matchId}/predictions` | 승부예측 생성 |

승부예측 생성:

- `pick`은 `home`, `draw`, `away` 중 하나다.
- `stakeAmount`는 필수이며 1 이상 정수다.
- `stakeAmount`는 대마포인트(`POINT`)로 차감된다.
- 대마포인트 잔액이 부족하면 `400 INSUFFICIENT_POINT_BALANCE`로 거절한다.
- 이미 시작했거나 종료된 경기는 `409 PREDICTION_CLOSED`로 거절한다.
- 같은 사용자는 같은 경기에 한 번만 투표할 수 있다.

승부예측 정산:

- 자동 정산 워커가 `PREDICTION_SETTLEMENT_INTERVAL` 간격으로 API-FOOTBALL 종료 경기를 확인해 1회만 정산한다.
- `PREDICTION_SETTLEMENT_WORKER_ENABLED=false`로 자동 정산을 끌 수 있다.
- 관리자 `POST /api/admin/worldcup/matches/{matchId}/predictions/settle` 호출로 수동 정산할 수도 있다.
- 요청 body의 `winningPick`, `result`, `pick` 중 하나로 결과를 지정할 수 있다.
- 결과가 없으면 API-FOOTBALL 스코어로 `home`, `draw`, `away`를 추론한다.
- 예측 실패자는 본인 `stakeAmount`의 10%를 환급받는다.
- 실패자 환급액을 제외한 전체 풀은 승리 진영 참여자가 각자 승리 진영 내 stake 비율대로 나눠 받는다.
- 정산 결과는 `prediction_settlements`, 대마포인트 지급/환급 내역은 `ledger_transactions`와 `wallet_balances`에 저장한다.
- 마지막 자동 정산 실행 상태는 `GET /api/admin/system/jobs`의 `worldcup-prediction-settlement` 레코드에서 확인한다.

라인업 응답:

```json
[
  {
    "teamId": "string",
    "coach": "string",
    "formation": "4-3-3",
    "players": [
      {
        "id": "string",
        "name": "string",
        "number": 7,
        "position": "FW"
      }
    ]
  }
]
```

`position`은 API-FOOTBALL의 `player.pos` 값이다. 값이 비어 있으면 서버도 비워서 내려준다.

### 6.5 지갑 내역

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/api/customer/ledger/recent` | 최근 거래 |
| `GET` | `/api/customer/ledger/calendar` | 월별 캘린더 집계 |
| `GET` | `/api/customer/ledger/transactions` | 거래 목록 |
| `GET` | `/api/customer/ledger/analysis` | 월별 수입/지출 분석 |
| `GET` | `/api/customer/features` | 기능 플래그 |

## 7. 셀러 API

| Method | Path | 설명 |
| --- | --- | --- |
| `POST` | `/api/auth/seller/login` | 셀러 GitHub OAuth 시작 URL 생성 |
| `POST` | `/api/auth/seller/logout` | 로그아웃 |
| `GET` | `/api/seller/me` | 셀러 프로필 |
| `GET` | `/api/seller/booths` | 셀러 부스 목록 |
| `GET` | `/api/seller/booths/{boothId}` | 부스 상세 |
| `PATCH` | `/api/seller/booths/{boothId}` | 부스 수정 |
| `PATCH` | `/api/seller/booths/{boothId}/status` | 부스 상태 변경 |
| `GET` | `/api/seller/booths/{boothId}/staff` | 부스 직원 목록 |
| `POST` | `/api/seller/booths/{boothId}/staff` | 직원 추가 |
| `PATCH` | `/api/seller/booths/{boothId}/staff/{staffId}` | 직원 수정 |
| `GET` | `/api/seller/booths/{boothId}/products` | 부스 상품 목록 |
| `POST` | `/api/seller/booths/{boothId}/products` | 상품 생성 |
| `GET` | `/api/seller/products/{productId}` | 상품 상세 |
| `PATCH` | `/api/seller/products/{productId}` | 상품 수정 |
| `PATCH` | `/api/seller/products/{productId}/status` | 상품 상태 변경 |
| `POST` | `/api/seller/products/{productId}/images` | 상품 이미지 등록 |
| `GET` | `/api/seller/products/{productId}/inventory` | 재고 내역 |
| `POST` | `/api/seller/products/{productId}/inventory/adjustments` | 재고 조정 |
| `GET` | `/api/seller/products/{productId}/purchase-limits` | 구매 제한 조회 |
| `PATCH` | `/api/seller/products/{productId}/purchase-limits` | 구매 제한 저장 |
| `GET` | `/api/seller/booths/{boothId}/orders` | 부스 주문 목록 |
| `GET` | `/api/seller/orders/{orderId}` | 주문 상세 |
| `PATCH` | `/api/seller/orders/{orderId}/status` | 주문 상태 변경 |
| `POST` | `/api/seller/orders/{orderId}/cancel` | 주문 취소 |
| `POST` | `/api/seller/orders/{orderId}/refund` | 주문 환불 |
| `POST` | `/api/seller/pickup-vouchers/verify` | 픽업 바우처 검증 |
| `POST` | `/api/seller/pickup-vouchers/{voucherId}/redeem` | 픽업 바우처 사용 |
| `POST` | `/api/seller/pay/barcodes/lookup` | 결제 바코드 조회 |
| `POST` | `/api/seller/pay/payment-intents` | 결제 intent 생성 |
| `POST` | `/api/seller/pay/payment-intents/{intentId}/capture` | 결제 capture |
| `POST` | `/api/seller/pay/payment-intents/{intentId}/cancel` | 결제 취소 |
| `POST` | `/api/seller/pay/payments/{paymentId}/refund` | 결제 환불 |
| `GET` | `/api/seller/booths/{boothId}/payments` | 부스 결제 목록 |
| `POST` | `/api/seller/booths/{boothId}/visits/verify` | 방문 검증 |
| `GET` | `/api/seller/booths/{boothId}/visits` | 방문 목록 |
| `GET` | `/api/seller/booths/{boothId}/ranking` | 부스 랭킹 |
| `GET` | `/api/seller/booths/{boothId}/inquiries` | 부스 문의 |
| `POST` | `/api/seller/inquiries/{inquiryId}/replies` | 문의 답변 |
| `POST` | `/api/seller/booths/{boothId}/notices` | 부스 공지 생성 |
| `GET` | `/api/seller/booths/{boothId}/dashboard` | 부스 대시보드 |
| `GET` | `/api/seller/booths/{boothId}/settlements` | 정산 목록 |
| `GET` | `/api/seller/settlements/{settlementId}` | 정산 상세 |
| `GET` | `/api/seller/booths/{boothId}/reports/sales` | 매출 리포트 |
| `GET` | `/api/seller/booths/{boothId}/reports/inventory` | 재고 리포트 |
| `POST` | `/api/seller/booths/{boothId}/exports` | 내보내기 작업 생성 |

## 8. 관리자 API

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/api/admin/dashboard` | 관리자 대시보드 |
| `GET` | `/api/admin/festivals` | 축제 목록 |
| `POST` | `/api/admin/festivals` | 축제 생성 |
| `PATCH` | `/api/admin/festivals/{festivalId}` | 축제 수정 |
| `GET` | `/api/admin/booths` | 부스 목록 |
| `POST` | `/api/admin/booths` | 부스 생성 |
| `PATCH` | `/api/admin/booths/{boothId}` | 부스 수정 |
| `GET` | `/api/admin/booth-categories` | 부스 카테고리 목록 |
| `POST` | `/api/admin/booth-categories` | 부스 카테고리 생성 |
| `PATCH` | `/api/admin/booth-categories/{categoryId}` | 부스 카테고리 수정 |
| `POST` | `/api/admin/maps` | 지도 메타 생성 |
| `GET` | `/api/admin/users` | 사용자 목록 |
| `POST` | `/api/admin/users/import` | 사용자 import |
| `GET` | `/api/admin/users/{userId}` | 사용자 상세 |
| `PATCH` | `/api/admin/users/{userId}` | 사용자 수정 |
| `GET` | `/api/admin/roles` | 역할 목록 |
| `POST` | `/api/admin/role-assignments` | 역할 할당 |
| `DELETE` | `/api/admin/role-assignments/{assignmentId}` | 역할 할당 삭제 |
| `GET` | `/api/admin/wallets` | 지갑 목록 |
| `POST` | `/api/admin/wallets/adjustments` | 지갑 조정 |
| `GET` | `/api/admin/ledger/transactions` | 전체 거래 목록 |
| `GET` | `/api/admin/ledger/exports` | 거래 내보내기 목록 |
| `POST` | `/api/admin/reward-rules` | 보상 규칙 생성 |
| `PATCH` | `/api/admin/reward-rules/{ruleId}` | 보상 규칙 수정 |
| `GET` | `/api/admin/notices` | 공지 목록 |
| `POST` | `/api/admin/notices` | 공지 생성 |
| `PATCH` | `/api/admin/notices/{noticeId}` | 공지 수정 |
| `GET` | `/api/admin/promotions` | 프로모션 목록 |
| `POST` | `/api/admin/promotions` | 프로모션 생성 |
| `PATCH` | `/api/admin/promotions/{promotionId}` | 프로모션 수정 |
| `POST` | `/api/admin/notifications` | 알림 발송 기록 생성 |
| `GET` | `/api/admin/worldcup/teams` | API-FOOTBALL 팀 목록 |
| `POST` | `/api/admin/worldcup/teams` | 로컬 팀 레코드 생성 |
| `GET` | `/api/admin/worldcup/matches` | API-FOOTBALL 경기 + 로컬 경기 |
| `POST` | `/api/admin/worldcup/matches` | 로컬 경기 생성 |
| `PATCH` | `/api/admin/worldcup/matches/{matchId}` | 로컬 경기 수정 |
| `PUT` | `/api/admin/worldcup/matches/{matchId}/lineups` | 로컬 라인업 저장 |
| `PUT` | `/api/admin/worldcup/matches/{matchId}/stats` | 로컬 스탯 저장 |
| `POST` | `/api/admin/worldcup/matches/{matchId}/predictions/settle` | 예측 정산 기록 생성 |
| `GET` | `/api/admin/worldcup/predictions` | 예측 목록 |
| `GET` | `/api/admin/audit-logs` | 감사 로그 목록 |
| `GET` | `/api/admin/system/health` | 시스템 상태 |
| `GET` | `/api/admin/system/jobs` | 시스템 작업 목록 |
| `POST` | `/api/admin/incidents` | 장애/사고 기록 생성 |
| `POST` | `/api/admin/ranking-rules` | 랭킹 규칙 생성 |

## 9. 상태 확인

### 9.1 API 상태

```bash
curl http://localhost:8080/healthz
```

### 9.2 시스템 상태

```bash
curl http://localhost:8080/api/admin/system/health
```

예시:

```json
{
  "data": {
    "api": "ok",
    "database": "ok",
    "payments": "not_configured",
    "apiFootball": "configured",
    "checkedAt": "2026-06-24T11:06:39+09:00"
  },
  "meta": {
    "requestId": "string",
    "serverTime": "2026-06-24T02:06:39Z"
  }
}
```

## 10. 보안/운영 주의사항

- `.env`는 git에 커밋하지 않는다.
- GitHub OAuth secret, API-FOOTBALL key는 `.env.example`에 적지 않는다.
- private repository commit 조회를 위해 GitHub OAuth scope에 `repo`가 필요하다.
- 운영에서는 `GITHUB_OAUTH_TRUST_REQUESTED_ROLE=false`를 유지한다.
- 셀러/관리자 권한은 GitHub login/email allowlist로 부여한다.
- API-FOOTBALL 경기 데이터는 fallback 없이 upstream 결과만 사용한다.
