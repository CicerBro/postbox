# HTTP rate limits and reverse proxies

Public password login, passkey login, subscription forms and confirmation links
use the canonical client IP address as their rate limit identity. Source ports
never form part of an identity. IPv4 mapped IPv6 addresses share the IPv4 bucket.
Authenticated TOTP enrollment, verification, removal and administrator resets,
plus passkey registration and revocation, use the acting user account ID.
Opening new connections or sessions does not reset these account limits.

The default trusts no reverse proxies. All forwarding headers are ignored unless
the direct socket peer matches an explicitly configured trusted proxy IP or CIDR.
Set `POSTBOX_TRUSTED_PROXIES` or `--trusted-proxies` to a comma separated list,
for example `127.0.0.1,::1` for a same-host proxy or `10.20.0.2/32` for a dedicated
proxy address. Invalid entries fail startup. Container deployments must configure
the actual socket peer as observed inside the container, which can be a bridge
or gateway IP instead of the host loopback address.

Configure every trusted proxy to overwrite incoming `X-Forwarded-For` on the
first ingress hop, or append the actual peer address when preserving a chain.
Only list proxies you control, and restrict direct access to the application.
Avoid trusting broad networks containing client devices. Headers are interpreted
from right to left, skipping trusted proxy hops and stopping at the nearest
untrusted IP. An earlier client supplied address, valid or malformed, cannot
override that hop.
`Forwarded` and `X-Real-IP` are ignored. Forwarded entries must be bare IPv4 or
IPv6 addresses without ports or zones. Malformed addresses within the inspected
trusted suffix, more than 32 hops, or more than 4096 bytes fall back to the
direct peer identity. Malformed direct peer
addresses use one shared `unknown` bucket.

Each process applies fixed one minute windows: password login and authenticated
factor checks allow 10 requests, passkey operations and subscription forms allow
20, confirmation links allow 30, and authenticated API keys allow 600. Limits
are in memory per process. Multiple processes require a shared external limiter
if a deployment needs aggregate limits. Expired buckets are removed during
requests, including rejected requests. At most 10,000 active identities are
retained; requests for new identities are denied while capacity is full, without
evicting existing throttles.
