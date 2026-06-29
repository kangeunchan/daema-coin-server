UPDATE github_commit_events
SET payload = jsonb_set(
        jsonb_set(payload, '{commitTimestamp}', payload->'occurredAt', true),
        '{occurredAt}',
        to_jsonb(created_at),
        true
    ),
    updated_at = now()
WHERE payload ? 'occurredAt'
    AND NOT (payload ? 'commitTimestamp');
