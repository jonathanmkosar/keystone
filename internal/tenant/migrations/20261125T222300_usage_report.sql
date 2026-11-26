-- The usage report (P11-02). See docs/architecture/usage-report.md.
-- internal/usagereport reads these tables and writes none.
--
-- A usage record names no chat: it carries the request id of the API call
-- that set it off, and the report finds the chat of the turn sent under
-- that request id. This index makes that a lookup, not a scan of every
-- turn, once for each request id in the period.
CREATE INDEX turns_by_request ON turns(request_id, seq);
