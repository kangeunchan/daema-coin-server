ALTER TABLE internal_accounts
    ADD COLUMN IF NOT EXISTS booth_id TEXT;

UPDATE internal_accounts a
SET booth_id = m.booth_id
FROM booth_members m
WHERE a.id = m.account_id
    AND m.status = 'active'
    AND (a.booth_id IS NULL OR a.booth_id = '');
