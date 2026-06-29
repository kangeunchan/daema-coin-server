# Daema Coin Backend ERD

작성일: 2026-06-25

이 ERD는 프로덕션 최종 스키마다. 애플리케이션 읽기/쓰기 경로는 도메인별 PostgreSQL 테이블을 사용하고, 범용 JSONB 저장소에 의존하지 않는다.

## 핵심 원칙

- 고객 인증은 GitHub OAuth identity로 관리한다.
- 관리자와 부스 계정은 내부 계정으로 관리한다.
- 부스 계정은 관리자가 발급하며, `booth_members`로 접근 가능한 부스를 제한한다.
- 지갑 잔액은 `wallet_accounts`에 보관하고, 모든 변경은 append-only `ledger_transactions`로 남긴다.
- 결제, 예측 참여, 예측 정산, 관리자 조정은 idempotency key와 트랜잭션으로 중복 처리를 막는다.
- OAuth 직후 아직 학생 프로필이 없는 고객 세션은 `auth_sessions.principal_id`로 보존하고, 프로필 FK는 연결 가능한 경우에만 채운다.
- 원본 세션 토큰은 저장하지 않고 해시만 저장한다. 로그아웃/만료 처리는 `auth_sessions.revoked_at`과 `expires_at`으로 판단한다.
- 감사가 필요한 관리자/부스 작업은 `audit_logs`에 남긴다.

## Mermaid ERD

```mermaid
erDiagram
    INTERNAL_ACCOUNTS ||--o{ AUTH_SESSIONS : "starts"
    INTERNAL_ACCOUNTS ||--o{ BOOTH_MEMBERS : "assigned"
    INTERNAL_ACCOUNTS ||--o{ AUDIT_LOGS : "acts"
    INTERNAL_ACCOUNTS ||--o{ FILE_UPLOADS : "uploads"

    CUSTOMER_PROFILES ||--o{ GITHUB_IDENTITIES : "links"
    CUSTOMER_PROFILES ||--o{ AUTH_SESSIONS : "starts"
    CUSTOMER_PROFILES ||--o{ WALLET_ACCOUNTS : "owns"
    CUSTOMER_PROFILES ||--o{ LEDGER_TRANSACTIONS : "receives"
    CUSTOMER_PROFILES ||--o{ ORDERS : "places"
    CUSTOMER_PROFILES ||--o{ PAY_BARCODES : "creates"
    CUSTOMER_PROFILES ||--o{ BENEFIT_CLAIMS : "claims"
    CUSTOMER_PROFILES ||--o{ WORLDCUP_PREDICTIONS : "makes"
    CUSTOMER_PROFILES ||--o{ GITHUB_COMMITS : "earns"

    GITHUB_IDENTITIES ||--o{ GITHUB_APP_INSTALLATIONS : "connects"
    GITHUB_IDENTITIES ||--o{ GITHUB_COMMITS : "authors"

    FESTIVALS ||--o{ BOOTHS : "contains"
    BOOTH_CATEGORIES ||--o{ BOOTHS : "groups"
    BOOTHS ||--o{ BOOTH_MEMBERS : "has"
    BOOTHS ||--o{ PRODUCTS : "sells"
    BOOTHS ||--o{ ORDERS : "fulfills"
    BOOTHS ||--o{ PAYMENT_INTENTS : "collects"
    BOOTHS ||--o{ PAYMENTS : "receives"

    PRODUCTS ||--o{ ORDER_ITEMS : "ordered"
    PRODUCTS ||--o{ INVENTORY_ADJUSTMENTS : "adjusted"

    ORDERS ||--o{ ORDER_ITEMS : "contains"
    ORDERS ||--o{ PAYMENT_INTENTS : "paid_by"
    PAYMENT_INTENTS ||--o{ PAYMENTS : "captures"
    PAYMENTS ||--o{ REFUNDS : "refunds"
    PAYMENTS ||--o{ LEDGER_TRANSACTIONS : "posts"

    WALLET_ACCOUNTS ||--o{ LEDGER_TRANSACTIONS : "posts"
    BENEFITS ||--o{ BENEFIT_CLAIMS : "claimed"

    WORLDCUP_TEAMS ||--o{ WORLDCUP_MATCHES : "home"
    WORLDCUP_TEAMS ||--o{ WORLDCUP_MATCHES : "away"
    WORLDCUP_MATCHES ||--o{ WORLDCUP_PREDICTIONS : "predicted"
    WORLDCUP_MATCHES ||--o{ PREDICTION_SETTLEMENTS : "settled"
    PREDICTION_SETTLEMENTS ||--o{ PREDICTION_SETTLEMENT_ENTRIES : "pays"
    WORLDCUP_PREDICTIONS ||--o| PREDICTION_SETTLEMENT_ENTRIES : "result"
    PREDICTION_SETTLEMENT_ENTRIES ||--o{ LEDGER_TRANSACTIONS : "posts"

    INTERNAL_ACCOUNTS {
        text id PK
        text login_id UK
        text password_hash
        text role
        text status
        text display_name
        boolean force_password_change
        timestamptz last_login_at
    }

    CUSTOMER_PROFILES {
        text id PK
        text display_name
        text school_name
        text student_no UK
        text status
    }

    GITHUB_IDENTITIES {
        text id PK
        text customer_id FK
        bigint github_id UK
        text login UK
        text email
    }

    BOOTHS {
        text id PK
        text festival_id FK
        text category_id FK
        text name
        text status
    }

    BOOTH_MEMBERS {
        text id PK
        text booth_id FK
        text account_id FK
        text role
        text status
    }

    WALLET_ACCOUNTS {
        text id PK
        text customer_id FK
        text currency
        bigint balance
        bigint version
    }

    LEDGER_TRANSACTIONS {
        text id PK
        text wallet_account_id FK
        text customer_id FK
        text direction
        text currency
        bigint amount
        text transaction_type
        text idempotency_key UK
    }

    AUTH_SESSIONS {
        text id PK
        text token_hash UK
        text principal_id
        text principal_type
        text customer_id FK
        text internal_account_id FK
        text role
        jsonb session_data
        timestamptz expires_at
        timestamptz revoked_at
    }

    ORDERS {
        text id PK
        text customer_id FK
        text booth_id FK
        text status
        text currency
        bigint total_amount
    }

    PAYMENT_INTENTS {
        text id PK
        text order_id FK
        text booth_id FK
        text status
        bigint amount
    }

    PAYMENTS {
        text id PK
        text payment_intent_id FK
        text order_id FK
        text booth_id FK
        text status
        bigint amount
    }

    WORLDCUP_PREDICTIONS {
        text id PK
        text match_id FK
        text customer_id FK
        text pick
        bigint stake_amount
        text status
    }

    PREDICTION_SETTLEMENTS {
        text id PK
        text match_id FK
        text winning_pick
        text status
    }
```

## 마이그레이션 순서

1. 정규 테이블과 `schema_migrations`를 생성한다.
2. 기존 레거시 JSONB 데이터가 있는 운영 DB는 `0002_backfill_legacy_resources_to_core_schema.sql`로 정규 테이블에 전량 백필한다.
3. 지갑 잔액은 `wallet_accounts.balance`와 `ledger_transactions` 합계를 대조한다.
4. 애플리케이션 읽기/쓰기 경로를 정규 테이블 repository로 전환한다.
5. cutover 검증 후 최종 스키마에 레거시 JSONB 저장소가 남아 있지 않은지 확인한다.

cutover 전 검증 쿼리는 `docs/database-cutover-checks.sql`을 사용한다.
