-- Remove deprecated OpenAI Cursor compat account flag.
-- Cursor compat is now always enabled on compat routes, so the persisted toggle
-- is obsolete and should be cleaned from historical account extras.

UPDATE accounts
SET extra = extra - 'cursor_session_compat_enabled'
WHERE extra IS NOT NULL
  AND extra ? 'cursor_session_compat_enabled';
