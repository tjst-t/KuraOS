# Roadmap Generation Log

**Generated**: 2026-05-07 (Phase 1 initial roadmap)
**Source**: docs/initial-input/Kuraos-design.md (1874 lines), docs/VISION.json, docs/DESIGN_PRINCIPLES.json
**Mode**: autopilot setup (autonomous, no user dialog)

## Design decisions

### Sprint count: 14
Large-scale project (Phase 1 alone covers 21 sections of the design doc). Per `sprint-roadmap.md`, large projects warrant 8-15 sprints — chose 14 to balance grain (one major Engine per sprint) against pace (each sprint delivers visible progress).

### Sprint ordering rationale
1. **S0ff37f Bootstrap → S464e47 UI shell + config skeleton → S1e7eeb Auth** — foundational triplet. Auth before features so every later UI is access-controlled from day one.
2. **Se3b190 → S9db742 → Sd64f38 (Storage read → Storage write → Share)** — Share depends on Volume which depends on Pool. Read-only first to test the CmdExecutor parsers without touching real disks.
3. **S1bccf5 → S65b510 → S822961 (Manifest+Registry → App lifecycle → OIDC)** — App ecosystem block. OIDC last because OIDC client autoregistration plugs into the install flow.
4. **S8a756d → Se1e7a6 → Sf92666 (Monitor+Notify → Backup → TLS+Network+Logging+SelfUpdate)** — operations block. Monitor first because Backup uses EventBus / notification channels.
5. **S0eedaa → S99702c (File API → Portal+Wizard+polish)** — user-facing finish. File API is positioned late because it's only valuable once apps + auth + shares + logging are in place.

### Milestones (4)
- **S1e7eeb** (foundational kura skeleton runs) — first runnable + auth-protected.
- **Sd64f38** (Storage + Share usable) — first end-user value: SMB share works.
- **S822961** (App ecosystem ready) — first "wow" demo: install immich.
- **S99702c** (Phase 1 complete) — full handover-ready.

Spacing: 3 / 3 / 3 / 5 sprints — last block intentionally larger because operations stories are independently parallelizable.

### Scope decisions

Excluded from roadmap (placed in `backlog`):
- en.json (v1.x)
- Driver model stage:any (v1.5) and scope:user (v2+)
- DNS providers other than Cloudflare (v1.x)
- WebDAV gateway (out_of_scope per VISION)
- OAuth Device Flow (out_of_scope for v1)
- Version rollback attack mitigation (v1.x per design.md §7.9)
- UPS / disk spindown (Phase 1 secondary)
- monitoring-stack app (registry-side work, not core)
- Cloudflare Tunnel / WireGuard (v1 ships tailscale only)
- Phase 2 migration

Excluded entirely (in VISION out_of_scope, never to be added):
- Collabora / OnlyOffice / CalDAV / CardDAV / public share links
- Mail / chat / groupware
- SAML / LDAP / SCIM / clustering
- MFA / WebAuthn / passkey (federate to external IdP instead)
- Desktop metaphor UI
- ZFS dedup / SLOG / L2ARC / native encryption (UI-managed)
- Third-party File API integration
- One-click OS rollback UI in v1
- Auto sync of app UI state into config.json

### Story format
All stories follow `{役割}として、{やりたいこと}をしたい。なぜなら、{理由}だから。` — verified across 32 stories.

### Acceptance criteria pattern
Each AC has a unique `AC-{StoryID}-{N}` ID and points to a test file path. GUI stories use `*.e2e.spec.ts` for Playwright; non-GUI stories use Go unit tests or shell acceptance tests under `tests/acceptance/`.

### Open notes
- The first sprint (S0ff37f) intentionally has no Tailwind setup — that lives in S464e47 to keep S0ff37f's scope minimal.
- ACME testing in S1f92666 will use lego's pebble-based local CA in CI (not real Let's Encrypt staging).
- `make serve` Makefile target requires `bin/kura` to exist; the bootstrap sprint creates that, so earlier dev iterations skip portman until then.
