# Dev mock OIDC Provider

Standalone OIDC Provider used by `tests/e2e/google-federation-flow.e2e.spec.ts`
to drive the full `/federation/google/{start,callback}` flow without
depending on the real Google OAuth platform.

## What it does

Minimal Core 1.0: discovery / authorize (auto-approve) / token / userinfo.
id_token is alg=none (matches v1 federation flow which trusts the userinfo
round-trip). Configured via env vars — defaults are wired in the systemd
unit. The mocked user is `e2e-google-user@oidc-mock.example.com` (subject
`e2e-google-user`) so federation provisioning + binding lookups have a
stable identity to assert against.

## Deploy to the dev VM

```bash
make oidc-mock-deploy
```

…which builds `bin/oidc-mock`, rsyncs to `/home/ubuntu/dev-oidc-mock/`,
installs the systemd unit, and enables/starts it. After this:

```bash
ssh ubuntu@192.168.1.42 'systemctl is-active dev-oidc-mock'  # active
ssh ubuntu@192.168.1.42 'curl -s http://127.0.0.1:9998/.well-known/openid-configuration'
```

## Wire kura to use it

kura's federation Manager reads `KURA_FED_GOOGLE_*` env vars at startup.
Restart kura with the mock pointed at:

```bash
ssh ubuntu@192.168.1.42 'sudo pkill -x kura; sleep 2; \
  cd /home/ubuntu/kuraos && sudo nohup env \
    KURA_PORT=8204 \
    KURA_STATE_DB=/home/ubuntu/kuraos/state.db \
    KURA_APP_KEYS_DIR=/var/lib/kura/app-keys \
    KURA_FED_GOOGLE_CLIENT_ID=kura-e2e-mock-client \
    KURA_FED_GOOGLE_CLIENT_SECRET=mock-secret-not-real \
    KURA_FED_GOOGLE_ISSUER=http://127.0.0.1:9998 \
    KURA_FED_GOOGLE_AUTO_PROVISION=1 \
    /home/ubuntu/kuraos/kura > /home/ubuntu/kuraos/kura.log 2>&1 </dev/null & disown'
```

Verify:

```bash
curl -s -I http://192.168.1.42:8204/federation/google/start   # 302 (was 404)
```

## Run the e2e flow

```bash
make e2e
```

The `google-federation-flow.e2e.spec.ts` test skips itself when the
federation endpoint isn't configured (302 not seen) so the suite still
passes in plain CI. With the mock configured it exercises the full
chain.

## Switch to real Google later (case A)

1. Register an OAuth client at https://console.cloud.google.com/apis/credentials
2. Authorized redirect URI: `http://192.168.1.42:8204/federation/google/callback`
3. Restart kura with the real client id / secret and `KURA_FED_GOOGLE_ISSUER`
   removed (defaults to `https://accounts.google.com`).
4. The e2e test is mock-only; the real Google flow is verified manually
   in a browser. Document the verification result in the sprint log.
