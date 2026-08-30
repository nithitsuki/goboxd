# Production deployment: goboxd behind Cloudflare for Code Royale

This guide deploys goboxd for [Code Royale](https://github.com/coding-royale/Code-royale).
It locks down the HTTP boundary that goboxd itself does not handle: bearer
authentication, cross-origin policy, per-client rate limiting, TLS, and DDoS
protection.

> **Why this is necessary.** goboxd's sandbox is hardened (nsjail + seccomp +
> cgroup v2), but its *HTTP boundary* was historically open: `internal/api/`
> shipped only panic recovery, request IDs, and structured logging. Anyone who
> could reach the port could submit code and burn compute. The transport
> protections in `internal/api/auth.go`
> ([`GOBOXD_AUTH_TOKEN`](#goboxd-environment-variables),
> [`GOBOXD_ALLOWED_ORIGINS`](#goboxd-environment-variables)) close that at the
> application layer, and the Cloudflare edge adds TLS/WAF/DDoS on top. Rate
> limiting is enforced at the edge and per-user in the frontend, not inside
> goboxd (goboxd has no in-process limiter).

## Architecture

```
Browser ──► Vercel (Next.js /api routes)
              │ GOBOXD_API_URL = https://goboxd.nithitsuki.com
              │ Authorization: Bearer $GOBOXD_AUTH_TOKEN   ← sent by frontend/src/lib/goboxd.ts
              ▼
        Cloudflare edge
          ├─ Universal SSL (TLS)         ── Forced HTTPS
          ├─ Managed WAF + DDoS          ── Free plan
          ├─ Rate limiting rule          ── per client IP (see below)
          └─ (optional) Access service token  ── second auth factor at the edge
              ▼  Cloudflare Tunnel
        VPS: cloudflared ──► 127.0.0.1:8080 ──► goboxd (needs --privileged + nsjail)
```

Key rule: **the browser never talks to goboxd.** Only the Next.js server does,
server-to-server. goboxd binds to `127.0.0.1`, so there is no public port; the
only path in is the Cloudflare Tunnel.

---

## 1. VPS prerequisites

goboxd executes untrusted code, so it must run on a host you fully control:

- Linux with Docker Engine (privileged containers) and `make` + `git`.
- **Root access.** nsjail needs Linux namespaces and, on systemd hosts, a
  writable cgroup v2 hierarchy.
- **Tight surveillance.** Treat this host as high-value: firewalled, no public
  ports except what Cloudflare needs (Tunnel dials *out*, so you can even block
  public inbound entirely). Only `ssh` (and outbound 443 to Cloudflare) are used.

Recommended sizing for Code Royale's 5 languages (see
[load-test results](loadtest/README.md)): **2 vCPU / 2 GB** is the floor;
4 vCPU / 4 GB is comfortable. goboxd's sustained breaking point was ~3 RPS on
2 vCPU / 2 GB with `GOBOXD_MAX_JOBS=4`.

## 2. Build the image (only Code Royale's languages)

Code Royale offers JavaScript (Node), Python, C++, Java, and C. Build the image
with just those to cut build time, image size, and surface area:

```bash
git clone --recurse-submodules https://github.com/nithitsuki/goboxd.git
cd goboxd
git submodule update --init --recursive
make build LANGS=py3,js,cpp,java,c
```

Each `LANG` family maps to one jail; the server advertises exactly these.

## 3. Run goboxd on the VPS (loopback + auth + rate limit)

Create a production compose overlay that does **not** publish a host port.
Use host networking so goboxd binds `127.0.0.1:8080` on the host, reachable by
`cloudflared` (Step 4):

`docker-compose.prod.yml`:

```yaml
services:
  goboxd:
    image: goboxd:latest
    ports:
      - "127.0.0.1:8080:8080"     # host loopback ONLY — tunnel reaches it, no public port
    privileged: true            # nsjail namespaces + cgroup v2
    restart: unless-stopped
    environment:
      - GOBOXD_AUTH_TOKEN=${GOBOXD_AUTH_TOKEN}   # REQUIRED
      - GOBOXD_ALLOWED_ORIGINS=${GOBOXD_ALLOWED_ORIGINS:-}
      - GOBOXD_MAX_JOBS=4
      - GOBOXD_LANGS=py3,js,cpp,java,c
      - GOBOXD_EXCLUDE_LANGS=${GOBOXD_EXCLUDE_LANGS:-}
```

Generate a random auth token and verify the container built from this branch,
then start it:

```bash
openssl rand -hex 32   # → put this in GOBOXD_AUTH_TOKEN (VPS + Vercel)
LANGS=py3,js,cpp,java,c docker compose -f docker-compose.prod.yml up -d --build
docker compose -f docker-compose.prod.yml ps
```

Confirm it is loopback-only and that the sandbox actually runs:

```bash
curl -s http://127.0.0.1:8080/info | jq .cgroupv2      # "active" ideal
curl -s http://127.0.0.1:8080/readyz                    # nsjail + languages OK
curl -s http://127.0.0.1:8080/info | jq .langs          # py3,js,cpp,java,c
```

cgroup v2: on systemd hosts run goboxd as **root** and with host cgroupns so
per-jail memory/pids limits are enforced. If `cgroupv2` reports `inactive`, the
runner falls back to rlimits (documented in `docs/security.md`); either is a
working configuration, but cgroup v2 is preferred.

## 4. Cloudflare Tunnel (private ingress)

Install `cloudflared`, authenticate to your zone (`nithitsuki.com`), create a
named tunnel, and point a DNS hostname at it. The tunnel dials *out* to
Cloudflare, so no inbound firewall port is needed and goboxd's loopback port
is never exposed.

```bash
# on the VPS
wget -q https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -O /usr/local/bin/cloudflared
chmod +x /usr/local/bin/cloudflared
cloudflared tunnel login

cloudflared tunnel create judge
cloudflared tunnel route dns judge goboxd.nithitsuki.com
```

Tunnel config `~/.cloudflared/config.yml`:

```yaml
tunnel: judge
credentials-file: /root/.cloudflared/<TUNNEL_ID>.json
ingress:
  - hostname: goboxd.nithitsuki.com
    service: http://127.0.0.1:8080
  - service: http_status:404   # block everything else
```

Run it as a service so it survives reboots:

```bash
cloudflared install
systemctl enable --now cloudflared
cloudflared tunnel run judge
```

Verify the public side answers with TLS:

```bash
curl -s -o /dev/null -w "%{http_code}\n" https://goboxd.nithitsuki.com/healthz   # 200
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://goboxd.nithitsuki.com/run   # 401
```

The `401` on `/run` (no bearer token) proves the whole chain works: TLS up,
tunnel reachable, goboxd gating `/run`.

## 5. Cloudflare edge hardening

In the dashboard for `nithitsuki.com`:

1. **Always Use HTTPS** — SSL/TLS → Edge Certificates → Always Use HTTPS = ON.
2. **Managed WAF** — Security → WAF → Managed Rules; enable the Cloudflare Managed
   Ruleset (default recommended set) so common attack traffic to
   `https://goboxd.nithitsuki.com` is dropped.
3. **Rate limiting** — Security → WAF → Rate limiting rules. Add a rule scoping
   *only* `host == goboxd.nithitsuki.com` and `method == POST` on `/run`, e.g.
   > 20 requests / 10 s per client IP → Block. This is the *distributed* choke
   point; goboxd's own per-IP limiter (Step 3) is the per-caller backstop.
   > Free-plan note: advanced/volume rate limiting is billed; the rule above is
   the basic per-IP form.
4. **(Recommended, defense-in-depth) Cloudflare Access service token** —
   If you want a second authentication factor that Cloudflare itself validates
   before traffic reaches your VPS:
   - Zero Trust → Access → Service Auth → create a service token.
     Note the `Client ID` / `Client Secret`.
   - Create an Access application for `goboxd.nithitsuki.com`.
   - Add a policy: **Allow** for the service token; **Deny** everyone else (Access
     is deny-by-default).
   - In Code Royale, add the two `CF-Access-Client-Id` /
     `CF-Access-Client-Secret` headers in `frontend/src/lib/goboxd.ts` alongside
     the Bearer header.

   This is optional but gives true zero-trust: even the Bearer secret, if it ever
   leaked, is insufficient because Access still demands its own token.

## 6. Code Royale (Vercel) configuration

Set these in the **Vercel project environment variables** (server-side only;
never exposed to the browser):

| Variable | Value |
|----------|-------|
| `GOBOXD_API_URL` | `https://goboxd.nithitsuki.com` |
| `GOBOXD_AUTH_TOKEN` | the same token set on the VPS |
| `SUBMIT_RATE_LIMIT` | 20 (max code submissions per user per window) |
| `SUBMIT_RATE_WINDOW_SECONDS` | 20 |

`frontend/src/lib/goboxd.ts` reads `GOBOXD_API_URL` (required) and sends
`Authorization: Bearer $GOBOXD_AUTH_TOKEN` when the token is set.

## 7. Frontend hardening (implemented)

Code Royale's `POST /api/practice/submit` is now hardened:

- **Authenticated first.** It calls `auth.getUser()` before any work and returns
  `401` when anonymous, so a rotating IP can't reach goboxd or join the
  submission budget.
- **Per-user (non-anonymous) rate limit.** Every request that reaches code
  execution draws from the user's own budget, enforced durably in Postgres
  (`submit_rate_limits` table + `bump_submit_rate()` RPC in
  `frontend/supabase-submit-rate-limit.sql`) — NOT per-IP, so shared IPs can't
  shield a single attacker and one user isn't blocked by neighbours. Applies to
  `run` and `submit` intents alike. Over-limit returns `429` + `Retry-After`.

  > The SQL (`supabase-submit-rate-limit.sql`) is **already applied** to the
  > `project_royale` Supabase project via the Supabase MCP as migration
  > `add_submit_rate_limit`, and verified live. Keep the file in the repo for
  > fresh environments / reproducibility. It is idempotent and creates only its
  > own table; it does not touch the ones owned by
  > `supabase-single-source-reset.sql`. Re-running the reset file drops app data
  > but does **not** drop the rate-limit table.

## 8. Verification checklist

| Check | Command | Expected |
|-------|---------|----------|
| Loopback-only | `ss -ltnp \| grep 8080` | bound to `127.0.0.1`, not `0.0.0.0` |
| Readiness | `curl -s http://127.0.0.1:8080/readyz` | `ok` |
| Auth required | `curl -s -o /dev/null -w "%{http_code}" -X POST https://goboxd.nithitsuki.com/run` | `401` |
| Auth works | `curl -s ... -H "Authorization: Bearer $GOBOXD_AUTH_TOKEN" -X POST https://goboxd.nithitsuki.com/run` | `200` (or judge result) |
| Browsers blocked | send request with an `Origin` header not in allow-list | `403` |
| Rate limit | burst of `/run` calls from one IP | `429` after capacity |
| Judge end-to-end | submit code in Code Royale practice | pass/fail per test |

## goboxd environment variables (transport layer)

| Variable | Effect | Default |
|----------|--------|---------|
| `GOBOXD_AUTH_TOKEN` | Require `Authorization: Bearer <token>` on `POST /run` | empty = auth off |
| `GOBOXD_ALLOWED_ORIGINS` | Comma list of origins allowed to make browser calls; any `Origin` not listed is rejected with 403 | empty = all browser origins rejected |
| `GOBOXD_RATE_LIMIT_RPS` | Sustained `/run` requests/sec per client IP (token bucket backstop) | empty = off |
| `GOBOXD_RATE_BURST` | Burst capacity for the limiter | `2 × RPS` |

Read-only endpoints (`/healthz`, `/readyz`, `/info`) stay open for probes.
`GOBOXD_AUTH_TOKEN` / `GOBOXD_ALLOWED_ORIGINS` are read per request, so
rotating the token needs no restart. The rate-limit env vars are read **once**
on first use and size the token buckets, so changing them does require a
restart. The limiter keys on `CF-Connecting-IP` (the edge-set client IP), which
is the only meaningful key behind the tunnel.

## Notes

- The `internal/api/auth.go` middleware (bearer + origin) is covered by
  `internal/api/auth_test.go`. Run `make test` to verify the full unit suite.
- Authentication is read from the environment **per request** (see `authConfig`),
  so rotating `GOBOXD_AUTH_TOKEN` takes effect immediately without a restart.
- For cgroup v2 preferred-on-systemd hosts, run the container as root with host
  cgroupns (see `docker-compose.prod.yml` notes and `docs/security.md`).