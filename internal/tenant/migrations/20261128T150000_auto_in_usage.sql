-- Auto in the usage report (P14-07). The report finds the request ids a
-- period's usage records were made under that the router routed, by a
-- join from those records to their verdicts, so each verdict is found by
-- its request id. See docs/architecture/auto-in-usage.md.
CREATE INDEX router_verdicts_by_request ON router_verdicts(request_id);
