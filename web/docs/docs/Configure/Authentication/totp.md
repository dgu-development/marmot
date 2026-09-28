---
title: Local two-factor authentication
description: Optional TOTP for local username and password accounts
---

# Local two-factor authentication

TOTP protects **local password login** with a code from an authenticator app or a one-use recovery code. It is off by default. SSO continues to follow the identity provider's MFA policy, including accounts with both a password and a linked provider. API keys, service accounts, MCP and CLI OAuth keep their existing authentication contracts; this is not an instance-wide MFA enforcement policy.

```yaml
auth:
  totp:
    enabled: true
    issuer: Marmot
```

Environment equivalents are `MARMOT_AUTH_TOTP_ENABLED` and `MARMOT_AUTH_TOTP_ISSUER`. Configure `MARMOT_SERVER_ENCRYPTION_KEY` using `marmot generate-encryption-key` before enrollment. TOTP uses the existing XChaCha20-Poly1305 encryptor and refuses plaintext storage even when pipeline `allow_unencrypted` is enabled. Preserve that encryption key; losing it requires administrative recovery. Turning the feature flag off disables the second-factor requirement for enrolled users too.

## Enroll and recover

1. Open **Profile → Two-factor authentication** and enter your current password.
2. Scan the QR code locally with your authenticator or enter the Base32 secret manually. Setup expires after ten minutes.
3. Confirm a six-digit code. Save the ten recovery codes immediately: the server stores only bcrypt hashes and never shows them again.
4. Future password logins request a new authenticator code or an unused recovery code. A code already used for confirmation cannot be reused for login in the same time window.

Disabling the factor requires the password and a valid second factor. Replacing recovery codes also requires both and immediately invalidates all previous recovery codes. Administrators with `users:manage` can reset a lost factor from **Users**. Confirming, disabling or administratively resetting a factor invalidates existing sessions. Confirmation and self-service disabling return a replacement session to the current browser so recovery codes remain visible.

SSO-only accounts cannot enroll. Secrets, verification codes and recovery codes must not be included in logs or support requests. Keep server and authenticator clocks synchronized.

## API and session contract

| Operation | Endpoint |
| --- | --- |
| Local login | `POST /api/v1/users/login` |
| Complete required password change | `POST /api/v1/users/login/password` |
| Complete second factor | `POST /api/v1/users/login/totp` |
| Own factor status | `GET /api/v1/users/totp` |
| Prepare enrollment | `POST /api/v1/users/totp/setup` |
| Confirm enrollment | `POST /api/v1/users/totp/confirm` |
| Disable factor | `DELETE /api/v1/users/totp` |
| Replace recovery codes | `POST /api/v1/users/totp/recovery` |
| Administrative reset | `DELETE /api/v1/users/totp/reset/{id}` |

With the flag enabled, a required password change returns `requires_password_change` and `mfa_token`; an enrolled account returns `requires_totp` and `mfa_token`. Neither response contains an `access_token`. The opaque 256-bit challenge is hashed in PostgreSQL, purpose-bound, expires in five minutes and is consumed once. It is deliberately **not a JWT**, so the normal session validator rejects it. A new login supersedes an older pending challenge for the same account. Password changes, session invalidation and factor reset invalidate pending challenges.

Send `{ "mfa_token": "…", "code": "…" }` to complete TOTP. A mandatory password change instead takes `{ "mfa_token": "…", "new_password": "…" }` and may return another challenge; clients must not assume it grants a session.

A five-minute per-account budget permits at most five failed second-factor attempts, persisted across restarts and new challenges. HTTP verification endpoints also rate-limit callers regardless of optional instance-wide rate-limit configuration. TOTP uses SHA-1, six digits, 30-second steps and ±1-step tolerance, with transactional replay prevention. Recovery consumption and challenge consumption are atomic.

## Fork migration and compatibility

This feature starts from fork `dgu` and adds `dgumigrations/007_user_totp.sql`. The parked WikiLLM branch also has an independent migration 007: **do not run this branch against that preview database**. Use a separate database for development. If both features are later integrated, renumber and reconcile the pending migrations before deployment.

The normal JWT now includes the exact session revocation epoch. Legacy tokens without that claim remain valid until revoked; if their second-resolution issuance time overlaps a revocation, they are rejected conservatively. SSO callbacks themselves are unchanged.
