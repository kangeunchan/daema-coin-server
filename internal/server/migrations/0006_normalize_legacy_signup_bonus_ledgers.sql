UPDATE ledger_transactions
SET reference_type = NULL
WHERE starts_with(idempotency_key, 'legacy-resources:ledger_transactions:')
    AND transaction_type = 'signup-bonus'
    AND direction = 'income'
    AND reference_type = 'signup-bonus'
    AND reference_id IS NULL
    AND (
        (currency = 'DMC' AND amount = 40000)
        OR (currency = 'POINT' AND amount = 10000)
    );
