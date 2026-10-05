# Security

## Principles

1. Least privilege.
2. No secrets in the mobile app.
3. No service-account private keys in Git.
4. Group isolation must be enforced by cloud access control.
5. Client-side checks are not sufficient for authorization.

## Credentials

Never ship:
- service account private keys
- Terraform credentials
- privileged GCP credentials
- long-lived administrative access tokens

## Group Access

The desired security property is:

```text
User A ∈ Group A
User A can access Group A

User A ∉ Group B
User A cannot access Group B
```

It is enforced by the backend, never only in the Flutter UI:

- The backend verifies the Google ID token (signature, issuer, audience, expiry, `email_verified`)
  **before** any GCS call. Being authenticated grants nothing.
- Group membership is an `active` entry with the caller's `sub` in `{group_id}/members.json`. A
  stranger asking about a group gets the same `404` as for a group that does not exist.
- Only the backend's service account has IAM access to the bucket. Members have none.
- The backend builds object paths itself; a client cannot name a path outside its group.
- Signed URLs are short-lived, per object; uploads are bound to content type, size range and the
  uploader header, so the uploader cannot be forged.
- `group_id` is a security boundary: validate it before use in a path (`^[a-z0-9][a-z0-9_-]{2,39}$`, never
  starting with `_`).
- The roster has a single writer; clients never receive `sub` or email.
- Group recovery (`GET /v1/my/groups`) returns only the caller's own active memberships; a stranger
  gets an empty list, so it reveals neither which groups exist nor who is in them.

## Reports, leaving and terms

- Reports are stored under the group prefix and are readable only by administrators; other members
  cannot see who reported whom.
- A member who leaves has `sub`, `email` and `nickname` erased from the roster. A member removed by an
  administrator keeps their row so the administrator can moderate and re-invite; the privacy policy
  says so.
- `redeem` requires the `terms_version` the app showed, so a client that skipped the terms screen
  cannot join.

## Administrator

The administrator is the owner, recognised by Google user id: the id(s) in the backend's `ADMIN_SUBS`,
which `setup.sh` sets to the account that ran it. An email address can change hands (a company or
school address, a recreated account); a user id never does, so once `ADMIN_SUBS` is set a matching
email grants nothing. Without `ADMIN_SUBS` (an older deployment, or an id that could not be read) the
backend uses the verified email in `ADMIN_EMAILS`. The backend logs which one is in force, never a
name. Administrators create groups, issue invitations and remove members.

The mobile app never receives project-owner/editor credentials, and there is no service-account key
file: the backend signs URLs through `signBlob` as its own service account.

## Invitations

A token is a bearer credential valid for the time the administrator picked (1 minute to 24 hours, default 5 minutes) and usable once (create-if-absent
marker). Redemption needs the joiner's own Google sign-in. An administrator can remove a member at
any time.

## Public endpoint

The backend is reachable from the internet. Mitigations: token check first, small max-instances,
bounded concurrency and request sizes, a budget alert. Volumetric floods are absorbed by Google's
front end; application-layer floods are billable and are only capped, not prevented.

## Logging

Do not log:
- access tokens
- refresh tokens
- private credentials
- unnecessary personal information

Operational logs should contain enough context to diagnose failures without exposing secrets.
