UPDATE customer_profiles
SET student_no = NULLIF(BTRIM(student_no), '')
WHERE student_no IS DISTINCT FROM NULLIF(BTRIM(student_no), '');

WITH duplicate_student_numbers AS (
    SELECT id
    FROM (
        SELECT
            id,
            ROW_NUMBER() OVER (
                PARTITION BY student_no
                ORDER BY created_at ASC, id ASC
            ) AS registration_order
        FROM customer_profiles
        WHERE student_no IS NOT NULL
    ) ranked_profiles
    WHERE registration_order > 1
)
UPDATE customer_profiles profiles
SET student_no = NULL,
    updated_at = now()
FROM duplicate_student_numbers duplicates
WHERE profiles.id = duplicates.id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_customer_profiles_student_no
    ON customer_profiles(student_no)
    WHERE student_no IS NOT NULL;

ALTER TABLE customer_profiles
    ADD CONSTRAINT customer_profiles_student_no_format_check
    CHECK (student_no IS NULL OR student_no ~ '^[0-9]{4,12}$')
    NOT VALID;
