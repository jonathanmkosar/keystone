"""Sanity check before enabling Cloudflare geo-block.

Per the spec: "Geo-block all non-US traffic at Cloudflare (verify Anthropic's
egress IPs are US-based first)."

This script tails recent /mcp request logs from the cloudflared service (or
asks Cloudflare's analytics API if a token is configured) and lists the
unique source ASNs / countries it has seen. If everything seen is US-based
ASNs you recognise (Anthropic, Google for mobile clients, your own ISP), it
is safe to enable a geo policy that allows only US traffic.

For now this script just prints the manual checklist; the actual log parsing
is left as a future enhancement once production traffic exists.
"""

from __future__ import annotations


CHECKLIST = """\
Pre-flight checklist before enabling Cloudflare geo-block (US-only):

1. In the Cloudflare dashboard, open Analytics & Logs > Traffic for each
   <stone>.<your-domain> hostname. Look at the last 7 days of requests.

2. Confirm every successful request originates from:
     - United States (Anthropic-controlled egress)
     - Your own home / mobile ISP, also US-based

3. If anything looks foreign-sourced, identify whether it's:
     - Mobile carrier NAT (Verizon/T-Mobile sometimes report odd PoPs) -> still US
     - Anthropic infra in another region -> file ticket with Anthropic before blocking
     - Actually-foreign noise -> confirm Cloudflare Access already rejects them

4. Once verified, in the Cloudflare dashboard:
     Security > WAF > Custom Rules > Create rule
       Name:      block-non-us
       Field:     Country
       Operator:  is not in
       Value:     United States
       Action:    Block
       Skip:      (leave unchecked)

5. Re-test the connector from your phone (US carrier) and from claude.ai web.

6. Watch the WAF events for 48h to catch false positives before declaring it stable.
"""


def main() -> int:
    print(CHECKLIST)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
