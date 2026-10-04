# Backend Design

Status: **target design, agreed but not yet implemented.** The Flutter code under `lib/` and the
Terraform under `terraform/` still implement the previous model (the app talks to GCS directly with
the user's own OAuth token, groups isolated by IAM Conditions). `Agents.md` describes the target
design; this document is the detail behind it. Decisions and their reasons are in
`docs/DECISIONS.md` (ADR-006 to ADR-011).

## 1. Why a backend

The previous model needed every user to register their own OAuth client, because Cloud Storage
scopes are sensitive and an Android OAuth client is bound to one package name + signing
certificate (unique across all Google Cloud projects). An app distributed through the Play Store
has one package name and one signing certificate, so it can only ever belong to **one** OAuth
client — the publisher's.

The fix is to stop asking Google for Cloud Storage access on the user's behalf. The app proves who
the user is (Google sign-in, `openid email profile` only — non-sensitive scopes, no OAuth
verification, no test-user cap, no 7-day token expiry). A small service in the **owner's own GCP
project** decides what that person may see and hands out short-lived signed URLs. Photos still go
straight between the phone and GCS.

## 2. Overview

```text
                 publisher (once)
   Play Store ── Flutter app ── one OAuth client (Android + Web), scopes: openid email profile
                    |
       Google sign-in -> ID token
                    |
        Authorization: Bearer <ID token>
                    v
   owner's own GCP project ─────────────────────────────────────────────
   |  Cloud Run service (Go, distroless)   <- "the backend"             |
   |     verify ID token -> check members.json -> list / sign URLs      |
   |         |                                                          |
   |         v (service account, no key file)                           |
   |  GCS bucket (Autoclass, soft delete)  <── signed URLs ── app       |
   ──────────────────────────────────────────────────────────────────────
```

Roles:

| Role | Who | Can |
|---|---|---|
| Owner / administrator | The person who contracted the GCP project and ran Terraform | Create groups, issue invitations, remove members. Registers **one** backend as their own. |
| Member | Anyone invited to a group | Browse, upload, delete photos and events of the groups they belong to, on any number of backends |

An ordinary member needs no GCP project. A user can be a member of groups on many backends but
owns at most one.

## 3. Authentication

1. The app signs in with Google (`google_sign_in`, Web client ID as `serverClientId`) and obtains an
   **ID token**. Signing in is also how the app "registers" the account: the account picker only
   offers real Google accounts, so the client never has to validate an address it was typed.
2. Every backend call carries `Authorization: Bearer <ID token>`.
3. The backend verifies, before touching GCS: signature (Google's published certs, cached), `iss`,
   `aud` equal to the publisher's Web client ID (`GOOGLE_CLIENT_ID`, public), `exp`, and
   `email_verified`. Failure -> 401.
4. The stable identity key is `sub`, stored only as a keyed hash (`HMAC-SHA256`, key derived from the backend secret); `email` is used for administrator matching in memory and never stored, nor is the name. Google puts email, name and picture in the ID token whatever scopes are requested (checked on Android), so they arrive in each request's header; they are not stored or logged.
5. Tokens live about an hour; the app refreshes them through `google_sign_in`'s silent
   re-authentication.

Being authenticated grants nothing. Authorization is the group roster (section 5), plus the
administrator check for owner-only operations:

- **Administrator** = the token's verified email is in `ADMIN_EMAILS` (environment variable).
  Terraform fills it with the email of the account that runs `terraform apply`
  (`google_client_openid_userinfo`), overridable by a variable. This is the person who holds the
  GCP contract.
- **Member of group G** = an `active` entry with the caller's `sub` in `G/members.json`.

A non-member asking about a group gets `404`, the same as for a group that does not exist.

Identity is fixed to the Google account. Changing the account in the profile screen drops that
account's groups from the device (see section 9); the new account must be invited again.

## 4. Bucket layout

One bucket per backend. Groups are prefixes; isolation is enforced by the backend, not by IAM
Conditions.

```text
{bucket}/
  _backend/secret.json                     invitation-signing secret (created once, create-if-absent)
  _backend/settings.json                   the owner's settings: limits on videos (section 17)
  {group_id}/members.json                  roster (section 5)
  {group_id}/invites/{nonce}               empty marker: this invitation was used
  {group_id}/reports/{report_id}.json      a member's report (section 7, "Moderation")
  {group_id}/reports/resolved/{report_id}  empty marker: that report is resolved
  {group_id}/members/{user_id}/favorites.json  that member's favorite folders and items (section 15)
  {group_id}/shares/{share_id}.share.json  a share-link manifest (section 16)
  {group_id}/{event_id}/event.json         the event: {"name": "...", "date": "YYYY/MM/DD"}
  {group_id}/{event_id}/original/{photo_id}.{ext}
  {group_id}/{event_id}/medium/{photo_id}.jpg
  {group_id}/{event_id}/thumbnail/{photo_id}.jpg
```

- `group_id` matches `^[a-z0-9][a-z0-9_-]{2,39}$` (3-40 characters) — in particular it cannot start with `_`, so it never
  collides with `_backend/`.
- `event_id` matches `^[a-z0-9]{8,32}$` and is checked on every route that takes one. Events sit in the same namespace
  as the group's reserved folders `invites/`, `reports/`, `members/` and `shares/` (6-7 characters each), so the length
  floor keeps an event id from ever being one of them; the backend also refuses those names explicitly (and the app
  never generates one).
- The date is deliberately not part of any path (ADR-016): changing an event's date overwrites `event.json` and
  moves nothing. The event listing reads `event.json`, so an event exists only if that object is present and valid
  (`name` 1-64 characters, `date` a real `YYYY/MM/DD`); folders without one are ignored.
- The naming convention is in `Agents.md` section 3.
- The bucket has uniform bucket-level access, public access prevention, 30-day soft delete and
  **Autoclass**. Objects under 128 KiB (thumbnails) stay in Standard; originals cool down as they
  stop being read. Only the backend's service account has access to the bucket.

### Uploader attribution

Each `original`, `medium` and `thumbnail` object carries the custom metadata
`x-goog-meta-uploader: <user_id>`. The backend puts that header **into the signature** when it
issues the upload URL, so a client that sends a different value gets a rejected upload: the field
cannot be forged. Object listings return custom metadata, so the uploader arrives with the list at
no extra request. No per-event author file is needed.

## 5. Roster: `{group_id}/members.json`

```json
{
  "version": 1,
  "display_name": "Family",
  "created_at": "2026-09-25T09:00:00Z",
  "members": [
    {
      "user_id": "550e8400-e29b-41d4-a716-446655440000",
      "sub": "h1:9f2c...",
      "nickname": "Taro",
      "status": "active",
      "joined_at": "2026-09-25T09:05:00Z"
    }
  ]
}
```

- `user_id` is issued by the backend when a person joins a group (per group, not per device), so it
  survives reinstalling the app or changing phones. Posts store `user_id`; screens show the
  roster's current nickname, so a nickname change updates every past post at once.
- `status` is `active`, `removed` (by an administrator), `suspended` (sharing was stopped because a
  plan lapsed, section 14; the roster header then also says `sharing: suspended`) or `left` (the member left). `suspended` behaves like `removed` for access
  (the member gets `404`) but marks the row as restorable by the administrator. A `removed`
  row stays as it is, so old posts still resolve to a nickname, and re-inviting the person
  reactivates the same row. A `left` row keeps only `user_id` and `joined_at`: **`sub` and
  `nickname` are erased**, and the client shows "left member" for it. Joining again later creates a
  new row (a new `user_id`); posts made under the old one stay attributed to "left member".
- **Only the backend writes this file**, always read-modify-write with a GCS generation
  precondition (`ifGenerationMatch`), retrying on `412`. Clients never write it, and never see
  `sub`: the API returns `user_id`, `nickname` and `status`.
- A group creator (administrator) is added as its first member.
- The backend may cache a roster in memory for a few seconds; a removal takes effect within that
  window. It is short, not zero — accepted.

## 6. Invitations

The administrator shows an invitation on screen: a **QR code** and the **same content as a string**
that can be copied to the clipboard and pasted into "Join with an invitation code" on the other
phone (for people who are not in the same room).

- Content: `{backend_url, token}`, encoded as one versioned string.
- Token: `payload = {v, group_id, nonce (random), exp}` plus an HMAC-SHA256 signature. The key is
  `_backend/secret.json`, generated by the backend the first time it is needed with
  `ifGenerationMatch=0` (create-if-absent, so two instances cannot disagree).
- **Lifetime: 1 minute to 24 hours, picked by the administrator with a time picker, default 5 minutes.**
  Checked against the backend's clock. A longer lifetime widens the window in which a leaked invitation
  can be used (it is still single-use), so the default stays the shortest useful one.
- **Single use.** Redeeming first creates `{group_id}/invites/{nonce}` with `ifGenerationMatch=0`;
  if that fails the invitation was already used. Then the roster is updated. If the roster write
  then fails, the invitation is spent and the administrator issues another one — never the other
  way round.
- Redeeming needs a valid ID token, so the joiner's hashed `sub` is recorded from their own
  sign-in, not typed by the administrator.
- The token is a bearer credential for at most a few minutes. That is the accepted trade: it makes
  invitations possible without collecting anyone's email address, and the administrator can remove
  a member at any time.
- While the invitation screen is open the administrator's app polls the member list every few
  seconds and shows "<nickname> joined". There is **no push notification**: FCM would need
  credentials from the publisher's Firebase project inside every owner's backend, or a relay run by
  the publisher. Nothing runs in the background.

## 7. API

### Recovering groups

A device that has lost its group list (reinstall, new phone, or a reviewer who was never handed an
invitation) can enter a backend URL and ask `GET /v1/my/groups`. The backend lists the bucket's
top-level prefixes (skipping `_backend/`), reads each `members.json` (bounded concurrency, a cap on
the number of groups scanned, short in-memory cache) and returns only the groups where the caller's
`sub` is an `active` member. The app then adds them exactly as if they had been joined, with the
roster's current nickname.

- It only restores existing memberships. It cannot join anything new: a stranger gets an empty list,
  and a removed member gets nothing.
- Cost grows with the number of groups on the backend, which is small for a personal backend.
- It makes reinstalling harmless: no new invitation is needed.
- Play review: the publisher runs a demo backend, has a reviewer Google account redeem one invitation
  in advance, and gives the reviewer the URL. See `docs/PLAY_STORE_CHECKLIST.md`.

All paths are under `/v1` except `/ping`. JSON in and out. Every endpoint except `/ping` requires
the bearer ID token. Errors: `401` (token), `403` (not an administrator), `404` (unknown or not
yours), `409` (already exists), `410` (invitation `invitation_expired` or `invitation_used`),
`422` (validation; also a malformed or forged invitation), `429` (too many open reports), `5xx` (retry). Write conflicts on
the roster (`412` from GCS) are retried inside the backend.

An administrator who is not a member of a group may still read it (`GET /v1/groups/{g}`), rename it,
invite to it and remove members, but cannot list its photos.

| Method and path | Who | Purpose |
|---|---|---|
| `GET /ping` | anyone, no token | `{"api_versions":[1], "build":"..."}`. Also wakes an idle instance. |
| `GET /v1/me` | any signed-in user | `{"is_admin": bool}`. Used when registering a backend URL. |
| `GET /v1/settings` | any signed-in user | The owner's settings (section 17). Read by the app before it uploads a video. |
| `PUT /v1/settings` | admin | Replaces the owner's settings. `403` for anyone else. |
| `GET /v1/my/groups` | any signed-in user | Groups where the caller is an `active` member: `group_id`, `display_name`, `user_id`, `nickname`. Empty list for a stranger (never an error). See "Recovering groups". |
| `POST /v1/groups` | admin | Create a group: `{group_id, display_name}` -> writes `members.json`. |
| `GET /v1/groups/{g}` | member | Display name, roster (`user_id`, `nickname`, `status`), the caller's `user_id`. Also how a device refreshes its member cache. |
| `PATCH /v1/groups/{g}` | admin | `{display_name}` -> updates the name in `members.json`. |
| `POST /v1/groups/{g}/invitations` | admin | `{ttl_minutes: 1-1440, default 5}` -> invitation string + expiry. |
| `POST /v1/invitations/redeem` | any signed-in user | `{token, nickname, terms_version}` -> joins the group; idempotent for an already-active member. A missing or malformed `terms_version` is `422 terms_required`, checked before the invitation is spent. A member who had `left` gets a new row and a new `user_id`. |
| `PATCH /v1/groups/{g}/me` | member | `{nickname}`. |
| `DELETE /v1/groups/{g}/members/{user_id}` | admin | Mark `removed`. |
| `POST /v1/groups/{g}/stop-sharing` | admin | Every `active` member except the administrator becomes `suspended`, in one roster write (`204`, idempotent). Used when a plan lapses (section 14). |
| `POST /v1/groups/{g}/restore-sharing` | admin | `{user_ids?}`: `suspended` members become `active` again (all of them when `user_ids` is omitted, which is how the app calls it). The group stops being suspended when none is left suspended. The app decides whether the group fits the plan. |
| `POST /v1/groups/{g}/leave` | member | The caller leaves: status `left`, `sub`/`nickname` erased (`204`). A second call gets `404`, like any non-member, because the row no longer says who the caller was. |
| `POST /v1/groups/{g}/reports` | member | `{target: {type: item, event: "<event_id>", photo_id} \| {type: user, user_id}, reason, note}` -> `201 {report_id}`. Sending exactly the same report again (same target, reason and note) returns the open report (`200`); another reason or note for the same target is a new report; at most 20 open reports per reporter (`429`); an unknown item or user is `404`, reporting yourself or a member who left is refused. |
| `GET /v1/groups/{g}/reports` | admin | Open reports, newest first, each with the reporter's and (for a user report) the target's current nickname, `status`, and for an item report its `original` object; never an email or `sub`. `?include=resolved` adds the resolved ones (with `resolved_at`). |
| `GET /v1/reports/open` | admin | `{groups: {"<group_id>": ["<report_id>", ...]}}`: the open reports of every group on the backend, from listings only (report files minus resolved markers). The administrator's app asks about once a minute while in the foreground. `GET /v1/groups/{g}` also carries `open_reports` for an administrator. Also carries `update_available: {version}` at most once (see "Update notice" below). |
| `POST /v1/groups/{g}/reports/{report_id}/resolve` | admin | Mark a report resolved (idempotent) and write its resolved marker. Deleting the reported item does not remove the report. |
| `GET /v1/groups/{g}/favorites` | member | The caller's own favorite folders and items in the group (section 15); empty when there are none. |
| `POST /v1/groups/{g}/favorites/ops` | member | `{ops: [...]}` (1-200) -> applies the operations to the caller's favorites in one write and returns the result (section 15). The whole batch is refused (`422`) if one operation is invalid. |
| `GET /v1/groups/{g}/events` | member | Events (`date`, `id`, `name`), sorted by date: one delimiter listing of `{g}/`, then each folder's `event.json` read in parallel. Reserved folders and folders without a valid `event.json` are skipped. |
| `POST /v1/groups/{g}/events` | member | `{date, id, name}` -> write `event.json` (create-only: a retry, or a second call, returns the stored event unchanged; `id` is 8-32 chars of `[a-z0-9]`, chosen by the app; `name` is 1-64 chars of text, no control characters; `date` a real `YYYY/MM/DD`). |
| `PUT /v1/groups/{g}/events/{id}` | member | `{name?, date?}`, at least one -> change the event's name and/or date (`422` if none or invalid, `404` if the event has no valid `event.json`). A read-modify-write of `event.json` guarded by a generation precondition and retried on conflict, so concurrent edits do not overwrite each other. Any member. Returns `{date, id, name}`. |
| `DELETE /v1/groups/{g}/events/{id}` | member | Soft-delete everything under the event prefix. Returns the number of objects. |
| `GET /v1/groups/{g}/events/{id}/items` | member | Items, oldest upload first: `photo_id`, `media_type` (image/video), `ext`, `size`, `uploaded_at`, `uploader` (`user_id`). |
| `GET /v1/groups/{g}/trash` | member | Soft-deleted media of the group still inside the retention window: `objects: [{path, generation, deleted_at}]`, only media objects (never the roster, invitations or reports); plus `events: [{id, name, date, restore?}]`, one entry per event any of those objects belong to, so the client can show each item's destination — `name`/`date` come from the event's live `event.json`, or, if that too was deleted, from the newest deleted copy, in which case `restore` names it (`{path, generation}`) so restoring it brings the event back along with its items. |
| `GET /v1/groups/{g}/trash/thumbnail` | member | `?path=&generation=` -> the raw JPEG bytes of one deleted item's thumbnail. A soft-deleted object has no signed URL, so this is the one place photo bytes pass through the backend; `path` must be a thumbnail object of the caller's group. |
| `POST /v1/groups/{g}/trash/restore` | member | `{objects: [{path, generation}]}` (at most 50) -> restores them, derivatives first, the original next, and a deleted `event.json` last. `404` once past the retention window. |
| `POST /v1/groups/{g}/uploads` | member | `{event_id, photo_id, ext, kinds}` -> signed PUT URLs for thumbnail, medium (a video's is its first frame) and original. `event_name` + `event_date`, when both are sent and valid, also create the event's `event.json` if it is missing (create-only), so an upload to an event whose creation call never got through still shows under its name. |
| `POST /v1/groups/{g}/downloads` | member | Batch of object paths -> signed GET URLs. |
| `DELETE /v1/groups/{g}/events/{id}/items/{photo_id}` | member | Soft-delete the item's original, medium and thumbnail (`204`; `404` if the original is gone). |
| `POST /v1/groups/{g}/shares` | member | `{items: [{event_id, photo_id, ext}], ttl_minutes: 5\|10\|15}` (1-30 items, default 10) -> `{share_id, url, expires_at}` (section 16). |

Who may delete stays as before: any member can delete photos and events; retention is GCS
soft delete.

### Update notice

The backend has no database and does no per-request network calls, so this stays true for update
checks too: `version` and `repo` (a Go repository string `owner/name`) are baked into the binary at
build time (`backend/Dockerfile`'s `VERSION`/`REPO` build args, set by `release-backend.yml` from the
release tag and `github.repository`). Right after `main` builds the `Server`, it starts one
background goroutine (`checkForUpdateAsync`) that asks GitHub's tag list
(`GET api.github.com/repos/{repo}/tags`) for the highest `backend-vX.Y.Z` tag and compares it to its
own `version`; a local/dev build (no build args, so `version == "dev"`) skips the check entirely,
since there is nothing meaningful to compare. This never blocks a request: the goroutine stores its
result (if any) in the `Server`, and `GET /v1/reports/open` — which an administrator's app already
polls about once a minute — hands it out via `update_available: {"version": "1.4.0"}` to whichever
call happens to arrive after the check completes.

The notice is consumed (cleared) the first time it is handed out, so one instance never repeats it.
Multiple Cloud Run instances (or a restart) each run their own check and could still hand out the
same version more than once across calls; rather than making the backend stateful to prevent that,
the app dedupes by version number in its own local database, the same way it already dedupes report
notifications (`report_notices`, section 9) — an accepted trade-off for keeping the backend's
"no database" rule (Agents.md section 1) intact.

### Moderation, leaving and deletion

Google Play treats invite-only sharing as user-generated content and asks for terms of use, in-app
reporting and account/data deletion (`docs/PLAY_STORE_CHECKLIST.md`). The publisher runs no server,
so all of it lives on the owner's backend.

- **Terms of use.** The app requires acceptance before the first join or upload (see section 9).
  `redeem` must carry the `terms_version` the app showed; the backend refuses a call without one, so
  an old or modified client cannot skip it. Only the version is checked; the acceptance itself is
  kept on the device.
- **Reports.** A member reports an item (`event` + `photo_id`) or a user (`user_id`) with a reason
  (`inappropriate`, `sexual`, `minor`, `harassment`, `spam`, `other`) and an optional short note. The backend writes
  `{group_id}/reports/{report_id}.json` (`report_id` a UUID, create-if-absent) holding the reporter's
  `user_id`, the target, the reason, the note, the time and `status: open`. Only administrators can
  list or resolve reports; the reporter's identity is not shown to other members. The administrator
  acts with the existing powers: delete the item, remove the member. Resolving a report rewrites its
  file (single writer, generation precondition) and creates the empty marker
  `{group_id}/reports/resolved/{report_id}`, so the open reports can be found from two listings
  without reading every report ever filed. A report file without a marker is read to be sure it is
  open; one found resolved (resolved before markers existed, or its marker write failed) gets its
  marker then, so only open reports are ever counted. The publisher never sees reports; if an
  administrator is the problem, members can leave the group.
- **No block function.** The app has no direct messaging: members only see each other's shared photos
  under a nickname. Reporting a user plus administrator removal cover the need. Revisit if direct
  interaction is ever added.
- **Leaving.** `POST .../leave` sets the caller's row to `left` and erases `sub` and
  `nickname`, and deletes their favorites document (section 15). Their photos stay and are shown as "left member"; a member who wants theirs gone
  deletes them before leaving (any member can already delete photos). A privacy page (not the app)
  tells people how to request deletion: leave the group, and ask the group's owner to delete photos
  or the whole project; the publisher holds no such data.
- **Data an administrator keeps.** A `removed` member's row (with the hashed `sub`) is kept so the
  administrator can moderate and re-invite; say so in the privacy policy.

### Signed URLs

- Signed by the backend's service account through the IAM Credentials `signBlob` API — **no key
  file exists anywhere** (`Agents.md` section 13).
- The backend builds every object path itself from `group_id` and the validated event and
  `photo_id`. A client cannot name a path outside its group.
- Upload URLs are `PUT`, bound to `Content-Type`, `x-goog-meta-uploader` and
  `x-goog-content-length-range` (so a URL cannot be reused for a larger file). Videos use a
  resumable upload: the URL is a signed `POST` with `x-goog-resumable: start`, and the client
  continues on the session URI it gets back.
- Download URLs are batched (`downloads` takes up to N paths) and used for **thumbnails, medium
  images and videos alike**. They are only needed on a device cache miss, so a grid full of cached
  thumbnails signs nothing. Default lifetime 1 hour; the app fetches a fresh URL if one has expired.
- Photo bytes never pass through the backend, so it does not need CPU or bandwidth sized for
  images, and uploads into GCS are free.

### Versioning

- The path carries the API generation (`/v1`). Within a generation, only additive changes.
- A backend serves the current generation and the **two before it (3 generations)**; `/ping`
  returns the range. The app checks it when it registers a backend and on each cold start.
- If the app's generation is outside the range: backend too old -> the app tells the member to ask
  their administrator to update the backend (members cannot); app too old -> update the app.

## 8. Cost and abuse controls

The service is reachable from the internet (authentication is application-level), so the aim is a
**ceiling on the bill**, not to stop a determined flood:

- Cloud Run `max-instances` small (e.g. 3), bounded concurrency, request-body size limit, batch
  size limits, no work before the token is verified.
- A budget alert in Terraform.
- Google's front end absorbs volumetric network-layer floods. Application-layer floods remain
  billable requests; the free tier (about 2M requests per month) covers normal use.
- Cloud Armor and a load balancer are deliberately not used (fixed monthly cost).

### Cold starts

Cloud Run scales to zero and stops an idle instance after about 15 minutes. The app:

- keeps the time of its last backend call **in memory** (a restart simply pings again);
- when the app **becomes active** and the last call is more than about 10 minutes old, sends
  `GET /ping` in the background, without waiting for user action;
- sends nothing while inactive or in the background, and no minimum instance is kept warm.

## 9. Flutter app changes

Screens (the rest of the UI is unchanged):

- **First launch** (new): Google sign-in, then a mandatory nickname screen that cannot be skipped,
  then the **terms of use** screen. The nickname is stored on the device and sent with `redeem`
  when joining a group; the accepted terms version is stored on the device and sent as
  `terms_version`. Nothing can be joined or uploaded until the terms are accepted, and the screen
  appears again when the terms version changes.
- **Empty state** (new): with no group and no backend, the first screen offers three entries and
  never a blank list: "join with a code" (QR or paste), "recover from a backend URL", and "register
  my own backend".
- **Profile** (new): signed-in Google account; nickname (changeable); the owner's backend (register / change /
  remove — at most one); backend and API version status.
  - Changing the Google account warns: groups and events seen with the previous account will no
    longer be visible and each group needs a new invitation; owner rights are lost unless the new
    account is the backend's administrator; unsent uploads must be finished first. The previous
    account's groups, member cache and thumbnail cache are then removed from the device.
  - Changing the nickname calls `PATCH .../me` for every group the person has on that backend.
- **Register backend** (new): enter the Cloud Run URL; the app calls `/ping` and `/v1/me` and shows
  whether the account is the administrator.
- **Create group** (administrator only) and **Invite** (administrator only): QR, string with a copy
  button, lifetime 5/10/15, live "joined" notice.
- **Join** (any user): scan the QR, paste the code, or **recover from a backend URL** (see
  "Recovering groups"). The screen shows the backend's host name before joining, because the person
  is trusting that backend with their photos.
- **Members** (administrator: remove; everyone: see nicknames; a member can **leave the group**,
  behind a confirmation that explains what is erased and that photos stay). "Report user" is in each
  member's menu.
- **Report content**: in the photo viewer's overflow menu; reason list, optional note, confirmation.
- **Reports** (administrator only): open and resolved reports (two tabs), with the target, reason
  and note; an item report shows the item's thumbnail, which opens it in the viewer. Buttons on an
  open report: delete the reported photo or video, remove the member, or mark resolved. A reload
  button and pull-down refresh the list.
- **Report panel** (administrator only): while any group of the administrator's own backend has
  open reports, "n reports are waiting" sits above every screen; tapping it opens the report list
  (after choosing the group when several have reports).
- **Photo viewer**: shows the uploader's nickname, and a favorite star at the top right of every photo and video (amber `#FFB300` with a purple gradient outline when filled).
- **Group list**: sorted by display name, favorite groups first; a star toggle per row. No network call; the name is refreshed when a group is opened.
- **Event list** (inside a group), top to bottom: (1) a fixed "Favorites" entry opening the group's
  favorited photos and videos across all its events, (2) favorite events, (3) all other events in
  chronological order.

Favorite groups and events are personal and stay in the device's SQLite; the backend has no part
in them. Favorite photos and videos are kept in favorite folders on the backend (section 15).

- **Favorites**: the fixed entry of the event list opens the member's folders (Default first). The menu makes a
  folder or switches to the rearranging mode (up/down buttons, a save button fixed at the bottom right).
- **Star of a photo or video**: opens a panel of folders as colour-changing buttons; register files the
  item in the chosen ones.

Local SQLite (still only a cache and the upload queue) changes: `groups.backend_url` in place of
`groups.bucket_name` (the URL is what identifies the backend, so there is no separate backends
table), a single `profile` row (nickname, accepted terms, `own_backend_url`, `account_email`),
`favorite_groups` / `favorite_events` / `favorite_items` / `favorite_folders` / `favorite_item_folders` / `favorite_ops` tables, and `backend_url` on the thumbnail
cache. The roster is read whenever a group is opened and kept in memory. Local records are keyed by
`group_id`, so joining a group whose id already exists on the device from another backend is
refused (ids are random, so this is not expected in practice). A group is added
to a device by an invitation or by recovery; the old "type the bucket name and group id" join is
removed.

New infrastructure code in the app: a backend API client (bearer token, error mapping, `/ping`
warm-up), and the signed-URL uploader/downloader replacing the direct GCS client. The
`devstorage.*` scopes, `GroupAdminService` and app-side bucket creation are removed.

## 10. Backend implementation

- Language: **Go**, a single static binary in a distroless image (fast cold start, small image).
  The app and backend need not share a stack.
- Lives in `backend/`. Depends on the Google Cloud Storage, IAM Credentials and ID-token
  verification libraries; each dependency is justified in its own commit.
- Stateless. No database; the only state is objects in the bucket (`Agents.md` section 7).
- Config by environment variables: `BUCKET`, `GOOGLE_CLIENT_ID`, `ADMIN_EMAILS`,
  `SIGNER_SERVICE_ACCOUNT`.

## 11. Terraform (owner runs it in Cloud Shell)

Creates: required APIs (Cloud Run, Storage, IAM Credentials, Artifact Registry), the bucket
(Autoclass, soft delete, uniform access, public access prevention), a dedicated service account
(object admin on that bucket only; `serviceAccountTokenCreator` on itself for `signBlob`), the Cloud
Run service (min instances 0, low max instances, the environment above, publicly invokable
because authentication is in the application: Cloud Run's invoker IAM check is disabled instead of
granting `allUsers`, which an organization's Domain Restricted Sharing policy would refuse), and an optional budget alert. Output: the service URL
to type into the app.

Terraform state lives in a small GCS bucket `<project_id>-tfstate` (Cloud Shell's home is not
permanent). `terraform/backend/setup.sh` creates the bucket if needed, points Terraform at it
and runs `terraform apply`; the owner's one command is the same for the first setup and for every
update (each run starts from a fresh clone, which carries the newest `backend_image`).
The container image is published by the project maintainer to GHCR (a public package, built by
`release-backend.yml` on every merge that changes the backend). Cloud Run pulls it straight from
GHCR: this was observed to work (section 12, item 5) but is not documented as supported by Google,
so the first deploy in a new owner's project is the thing to watch.

## 12. Prototype verification

Run on 2026-09-25 against a real project with the code in `backend/` and `terraform/backend/`
(verification resources were deleted afterwards). The ID token used was a real Google ID token from
`gcloud auth print-identity-token`, so the token *verification* path is real; the Android
`google_sign_in` path is not.

| # | Item | Result |
|---|---|---|
| 1 | ID token verification, and getting the token on Android | Works. On the Android emulator (Play services, debug key registered) `google_sign_in` 7.x `authenticate()` with **no scopes** showed only the account chooser ("to continue to sharepi"), returned an ID token with `aud` = the Web client ID, `email_verified: true`, about 1 hour lifetime, and the deployed backend accepted it (`/ping` 200, `/v1/me` `is_admin: true`; missing/garbage token -> 401). `attemptLightweightAuthentication()` returned the same token while it was still valid, and after it expired (about 1 hour) returned a **new** one that the backend accepted. It shows a brief "Signing you in" sheet, so the app should refresh only when the token is near expiry, not before every call. **Not yet checked:** a release/Play-signed build, a device without a signed-in Google account. |
| 2 | `google_client_openid_userinfo` gives the running account's email | Works (local ADC; `terraform apply` printed the account as administrator, `/v1/me` returned `is_admin: true`). Not yet run inside Cloud Shell itself. |
| 3 | `signBlob` batch latency | Fine: about 0.4 s for 10 URLs, 0.3-0.4 s for 50-100, 0.7 s for 200 (signed in parallel). No need for long-lived URLs. |
| 4 | Signed headers | Works: a forged `x-goog-meta-uploader` or a different `Content-Type` is rejected (403); a correct upload stores `uploader=<user_id>` and it comes back on the signed download. **Not yet checked:** enforcement of `x-goog-content-length-range`, resumable upload (signed `POST`). |
| 5 | Pulling a public image | Cloud Run pulled images straight from **Docker Hub and GHCR** (no Artifact Registry remote repository needed). Observed to work; not confirmed as officially supported. |
| 6 | Cold start | About 0.7 s for a request to a freshly started instance, about 0.1 s warm. Idle-then-request measurement pending. |
| 7 | Autoclass | Bucket created with Autoclass (terminal class defaults to Nearline). Interaction with soft delete and current pricing not yet checked. |

Terraform lessons: the provider's default is CPU always allocated (needs 512 MiB and costs more), so the
stack sets `cpu_idle = true` (CPU billed only while handling a request) and `startup_cpu_boost = true`;
128 MiB is enough for the Go image.

## 13. Out of scope

Push notifications, a shared database (Firestore is the fallback if per-photo metadata, search or
very large events are ever required; SQLite inside the container was rejected because Cloud Run's
disk is ephemeral), server-side image processing (thumbnails and medium images are still generated
on the device), a publisher-run central server, public distribution beyond invited members.

## 14. Plans and billing

Status: implemented (backend and app), with Google Play Billing replaced by a stand-in until the app
has a Play Console entry (see "Google Play Billing").

The administrator's plan sets how many people **a group** may hold and how many groups can be live. Member limits count `active` members **including the administrator**. They live
in an app configuration (`PlanConfig`), not in business code, so they are easy to change.

| Plan | Bought as | Google Play product | Members per group | Live groups |
|---|---|---|---|---|
| Free | - | - | 7 | 20 |
| Pro | one-time purchase | `plan_pro` (non-consumable in-app product) | 15 | 30 |
| Max | one-time purchase | `plan_max` (non-consumable in-app product) | 20 | 40 |
| Business | subscription | `plan_business` | 30 | 50 |
| Business Pro | subscription | `plan_business_pro` | 50 | 100 |
| Business Max | subscription | `plan_business_max` | unlimited | unlimited |

Owners of a one-time plan get two incentives, both chosen by the app (`PlanConfig.offerFor`), which
asks Play for the matching product: Pro owners buy Max through `plan_max_upgrade` (a one-time
product priced at the difference; owning it counts as Max), and owners of Pro or Max get a free first
month on any subscription (a developer-determined offer on its base plans). Full registration details
are in `docs/PLAY_CONSOLE_PRODUCTS.md`.

The limits are the same worldwide; prices are per country (`docs/PLAY_STORE_CHECKLIST.md`). A
subscription has a monthly and a yearly base plan under one product id, so the app treats both as the
same plan. The app shows no amount; Google Play's purchase sheet does.

### Source of truth: Google Play, trusting the Play account

- There is no publisher server and no purchase verification against the Play Developer API (that API
  needs the publisher's credentials, which must never reach an owner's backend or the app). The app
  asks Google Play what the device's **Play account** owns and trusts the answer.
- The purchase belongs to the Play account, not to the Google account used to sign in to the app.
  They are not matched (`obfuscatedAccountId` is not enforced). Signing in with another Google account
  clears the remembered plan, and the next check finds it again from the Play account.
- The app queries owned in-app products and subscriptions on launch, on resume, and about once an
  hour while the app is in the foreground. Nothing runs in the background. The plan is the
  **highest** owned: Business Max > Business Pro > Business > Max > Pro > Free.
- The Pro one-time product is never consumed, so it stays owned and is restored on any device signed
  in to the same Play account. Refunded purchases stop being returned.
- The app acknowledges a purchase itself once it is `PURCHASED` (unacknowledged purchases are refunded
  after 3 days); `PENDING` purchases grant nothing.

### Google Play Billing (stand-in until there is a Play Console entry)

The app talks to Play only through `PlayBillingGateway` (`queryOwned`, `purchase`). Three
implementations exist or are planned:

- `FakePlayBilling` (debug builds): ownership is held in memory and can be set by hand. The profile
  screen shows a debug panel to own any plan, go offline, move the clock forward a day or three, and
  run a check. The unit and controller tests use it too.
- `UnavailablePlayBilling` (release builds for now): always answers "cannot tell". Everyone stays on
  the free plan and nothing is ever stopped.
- The real gateway, wrapping the `in_app_purchase` plugin: **not written yet**. It needs a Play
  Console entry to be tried, and the dependency is added with it.

`AppClock` is the time the plan rules read; the debug panel moves it, and nothing else does.

### Enforcement is in the administrator's app only

The backend belongs to the owner, who can change its code, so it knows nothing about plans. It only
keeps the group's *state* (`sharing: suspended`, members `suspended`) and refuses invitations and joins
to a suspended group (`409 sharing_suspended`).

- Creating a group is refused when the plan's live-group limit is already reached. Suspended groups
  do not count.
- Issuing an invitation is refused when `active members + invitations issued by this device that are
  still valid >= the member limit`, and the app offers the upgrade instead.
- Members' apps do not check anything. A modified app or backend can bypass the limits; this is
  accepted.

### Local plan state (SQLite `profile`)

| Field | Meaning |
|---|---|
| `plan` | the plan last seen from Google Play (UI, offline) |
| `plan_last_active_at` | when Google Play last **confirmed** the plan was in force. `null` if the app has never seen a plan above Free |

### When a subscription ends

Google Billing does not tell the app *when* a subscription ended, so the app measures the time itself
from `plan_last_active_at`:

1. On each check, if Google Play returns the plan, the app sets `plan_last_active_at = now`.
2. If Google Play (a successful query, repeated once on a fresh billing connection) no longer returns
   what the app had seen (`plan_last_active_at` is not `null`, and the plan owned is lower):
   - **less than 3 days** since `plan_last_active_at`: sharing continues and the app shows a warning
     ("the plan could not be confirmed; sharing stops in N days");
   - **3 days or more**: the app **stops sharing where the remaining plan cannot hold it**.
3. It never does this on an error, when offline, or when `plan_last_active_at` is `null`.
4. It runs when the administrator's app checks. If the administrator never opens the app, the
   backend keeps working; nothing runs server-side.

**Which groups are stopped.** Take the plan that remains (Free, or Pro or Max if a one-time product is
owned). A live group survives only if it fits the member limit; of the groups that fit, the **oldest
ones survive, up to the plan's group limit** (20 on Free), by the roster's `created_at`. Every other
live group is stopped, and groups that are already stopped are left alone. For each stopped group the
app calls `POST /v1/groups/{g}/stop-sharing`: everyone except the administrator becomes `suspended`
(rows and `user_id`s are kept) and the group is marked suspended. The app then shows an alert: **"N
groups had their sharing stopped"** with the reason and how to bring them back.

If a stop call fails or a group cannot be read, the remembered plan is not updated, so the next check
finds the lapse again and finishes (stopping is idempotent).

### Editing and bringing back a stopped group

A stopped group keeps working for its administrator:

- The **administrator can still edit its members**: a `suspended` member can be removed
  (`DELETE .../members/{user_id}`).
- A stopped group is brought back **automatically, all at once**, as soon as everyone still in it
  (the administrator plus the `suspended` members) fits the plan's member limit **and** the live
  groups are below the plan's group limit. Older groups come back first.
- The app looks for this after the administrator removes a member from a stopped group, and on every
  plan check. The same rule covers a plan coming back: getting the subscription back (or a better plan)
  restores every stopped group that then fits. There is no partial restore and no prompt.
- To do it the app calls `POST /v1/groups/{g}/restore-sharing` without `user_ids`. Because `suspended`
  rows stay in the roster, no local list is needed, and it works after a reinstall or on another
  device. No member action and no new invitation is needed.
- While a group is stopped the app offers no invitations for it.

### Known trap: more than one device or Play account

If the device's Play account changes, or an administrator uses several devices with different Play
accounts, the plan can look lapsed and sharing may be stopped. The repeated query, the
`plan_last_active_at` guard, the 3-day margin and the fact that `suspended` is reversible are the
mitigations. The terms recommend **one administrator, one device**, and say the publisher takes no
responsibility for interruption.

## 15. Favorite folders (personal, kept on the backend)

A member's favorite photos and videos are filed in **favorite folders** they make and name in each
group. They are personal, but they must survive a reinstall and come back when a group is recovered, so
they are kept in the bucket, not only on the device (ADR-017).

### Storage

`{group_id}/members/{user_id}/favorites.json`, where `user_id` is the member's id in the roster. `members`
is a reserved folder name in the group's namespace, like `invites` and `reports`: it is 7 characters,
below the 8-character floor of an event id, the backend refuses it as an event id, the app never
generates it, and the trash lists media only, so it can never be listed or restored as a photo.

```json
{"version": 1,
 "folders": [{"id": "k3j9x0a1b2c4", "name": "Trips"}],
 "items": [{"photo_id": "uuid", "event": "k3j9x0a1b2c4", "path": "grp/evt/original/uuid.jpg",
            "folders": ["default", "k3j9x0a1b2c4"], "added_at": "2026-09-25T10:00:00Z"}]}
```

`folders` is in the member's display order. The **default** folder is implicit: it is never in `folders`,
its id `default` cannot be a folder id (ids are `^[a-z0-9]{8,32}$`), and it cannot be renamed, deleted or
moved. An item is stored only while it is in at least one folder. `path` must be the original object of
`photo_id` under this group and event, so a favorite can never point outside the caller's group.

### Operations

The app sends changes, not documents; the backend is the only writer. `POST .../favorites/ops` reads the
document, applies the operations in order and writes it with `ifGenerationMatch`, retrying on a conflict
(`409` after six), so two devices never overwrite each other. The answer is the resulting document.

| `op` | fields | Effect |
|---|---|---|
| `folder_create` | `id`, `name` | Appends the folder; an existing id is left as it is (idempotent). |
| `folder_rename` | `id`, `name` | Renames it; an unknown id is ignored (deleted on another device). |
| `folder_delete` | `id` | Removes the folder from the list and from its items; an item left in no folder is dropped. |
| `folder_order` | `ids` | Listed folders first, in this order; unknown ids are ignored, the rest follow in their old order. |
| `item_set` | `photo_id`, `event`, `path`, `folders`, `added_at` | Files the item in exactly `folders` (folders that do not exist are ignored); empty removes it. An existing item keeps its `added_at`. |

Names are 1-30 characters without control characters. Limits: 100 folders, 5000 items, 200 operations per
request. Every operation is safe to send twice, so a batch whose answer was lost is simply sent again. A
request with one invalid operation is refused whole (`422`) and writes nothing. `GET .../favorites`
returns the caller's own document only; there is no way to read another member's.

Leaving a group deletes the document. Removing a member does not (a re-invited member finds their folders
again), like the roster row.

### In the app

The screens read a local copy (SQLite, section 9) and change it at once. Each change is also queued
(`favorite_ops`); the sync sends the queue as one batch and then replaces the local group's favorites by
the returned document, unless a change was made in the meantime (then the next round sends it). Local
changes and the replacement are serialized so they never interleave. A network or server failure leaves
the queue and the copy alone and shows nothing; a batch refused as invalid is dropped, since sending it
again could not succeed. The sync runs when a group is opened, after each change, and after a group is
recovered or joined. Items that appear from the backend and whose photo or video has been deleted are
dropped, without an error, and the backend is told.

## 16. Share links

A member can hand a few photos or videos to someone outside the group — someone who is not invited and
may not even have the app — as a short-lived link, instead of exporting original files through the OS
share sheet (which the app also offers, unrelated to this section: download the originals to the device,
then hand them to whatever app the user picks).

### Why no new authorization mechanism

`POST /v1/groups/{g}/shares` is a member-only call, but what it returns is not gated by membership at
all: it is the same object-scoped, short-lived signed URL the backend already hands out for uploads and
downloads (section 7, "Signed URLs"), just handed to someone who is not in `members.json`. The signed URL
*is* the access grant, exactly as an invitation token is a bearer credential for a few minutes (section
6). Nothing new is exposed to the internet: there is no public, unauthenticated endpoint, no server-side
image processing, and no page that lists a group's photos to a stranger. The only new backend behaviour is
signing a few more URLs than a normal download batch and writing one small manifest object.

### Why a manifest, not the URLs themselves, in the link

A deep link has to survive being pasted into a chat app, a Play Store install flow (see "Reaching someone
without the app" below) and, on Android, the Play Install Referrer string — all of which are happier with
a short string. A signed URL is a few hundred characters; several of them end to end would not be. So the
backend signs the chosen items and writes them into one small object,
`{group_id}/shares/{share_id}.share.json` (`shares` is a reserved folder name like `invites`, `reports` and
`members` — section 4), and signs *that* object's URL. The link the app hands out is always exactly one
signed URL, whether it carries one photo or thirty.

```json
{
  "version": 1,
  "created_at": "2026-09-29T09:00:00Z",
  "expires_at": "2026-09-29T09:10:00Z",
  "items": [
    {
      "path": "grp_bbq2026/k3j9x0a1b2c4/original/550e8400-e29b-41d4-a716-446655440000.jpg",
      "media_type": "image",
      "original_url": "https://storage.googleapis.com/...",
      "medium_url": "https://storage.googleapis.com/..."
    }
  ]
}
```

`POST /v1/groups/{g}/shares` takes `{items: [{event_id, photo_id, ext}], ttl_minutes: 5|10|15}` (1-30
items, default 10 minutes — the app does not offer a longer choice, since it is not the joiner's dwell
time this has to survive, see below), signs each item's `original` and `medium` for that same lifetime,
writes the manifest (create-if-absent; the id is a fresh UUID, so this never conflicts in practice), signs
the manifest's own URL for the same lifetime, and returns `{share_id, url, expires_at}`. `url` is what the
app wraps in the deep link.

### Reaching someone without the app

The link is an Android intent URL: `sharepi://share?u=<the manifest's signed URL>` as the primary scheme,
wrapped with `S.browser_fallback_url` pointing at the Play Store listing (with a `referrer` query parameter,
see below) so that opening the link on a phone without the app falls through to the store instead of
failing silently — entirely client-side, no server-hosted `assetlinks.json`. Android App Links / iOS
Universal Links were considered and rejected here: both need a real domain serving a verification file over
HTTPS, which is exactly the publisher-run server this project deliberately has none of (section 1, section
13 "Out of scope"). A plain custom URI scheme has no such requirement and costs nothing to host. The
trade-off is accepted for MVP: iOS gets the same `sharepi://` scheme but no store fallback (a broken link on
iOS without the app just does nothing), consistent with iOS being out of the initial deployment scope
(Agents.md section 15).

If the recipient already has the app, it opens straight to a read-only preview of the manifest's items
(medium first, original on request, exactly like the normal viewer) built entirely from the URLs in the
manifest — no backend call, no membership check. If the signed URL has expired, GCS answers `403`, which
the app shows as "共有期限が切れています。共有者にリンクの再発行を依頼してください" (the invitation-expiry
message, adapted).

If the recipient does not have the app, Android's fallback opens the Play Store with a `referrer` query
parameter marking the install as coming from a share link. The **same manifest URL is also included in that
referrer string** (not just a flag): on first launch, the app reads it through the Play Install Referrer
API and, if the manifest's signed URL is still valid (installs from a store visit are usually well within
the 10-minute window), shows the shared items exactly as if the link had been opened directly; if it has
since expired, the app shows the same "ask for a new link" message. No signed URL is issued with a longer
lifetime for this: the 10-minute window is not extended, only carried across the install. There is no
equivalent on iOS (no Install Referrer API, and a from-scratch deferred-deep-link service is out of scope
here); an iOS install from the fallback is just a plain app install with no shared items attached.

### Cost and abuse

No new public, unauthenticated backend endpoint is introduced — the abuse surface stays what it already was
for downloads and uploads (section 8). The manifest object itself is small (well under the thumbnail size)
and readable only by whoever holds its short-lived signed URL, so even an adversary who somehow obtained a
link can only read it, and only until it expires. `google_storage_bucket.photos` in `terraform/backend/`
carries a lifecycle rule (`matchesSuffix: [".share.json"], age: 1 day, action: Delete`) that clears manifest
objects out of the bucket after a day; this is storage-cost hygiene, not the access boundary — that is
always the signed URL's own expiry, already gone within minutes.

---

## 17. Owner settings (limits on videos)

A video is by far the largest thing a member uploads, so the owner — who pays for the storage — sets
how large and how long one may be. The settings are one small object, `_backend/settings.json`,
written only by `PUT /v1/settings` with a generation precondition (single writer, Agents.md section
7). A backend that was never configured uses the defaults below.

```text
{"version": 1, "video": {"max_seconds": 300, "max_bytes": 0, "confirm_seconds": 120, "confirm_bytes": 0}}
```

| Field | Meaning (0 = no limit) | Default |
|---|---|---|
| `max_seconds` | A longer video is not uploaded. | 300 |
| `max_bytes` | A larger video file is not uploaded. | 0 |
| `confirm_seconds` | In the app's "full" mode a longer video asks: as it is, or compressed. | 120 |
| `confirm_bytes` | The same, by size. | 0 |

- **Size is enforced by the backend, length by the app.** The signed upload URL of a *video original*
  carries `min(backend limit, max_bytes)` as its size range, so a modified app cannot send a larger
  file. A video's length is not visible to the backend: `max_seconds` is the app's check, accepted the
  same way as the plan limits (Agents.md section 20). Photos, and a video's thumbnail and medium, are
  not affected.
- **Validation.** All values are whole numbers `>= 0`; seconds at most 24 hours; bytes at most the
  backend's own upload limit; a confirmation threshold may not exceed its maximum. Otherwise `422`.
- **The app reads them before every upload batch that has a video** (`GET /v1/settings` on the group's
  backend; the last answer is kept for when the backend cannot be reached) and asks the user — full or
  compressed — for a video over a confirmation threshold, or over `max_bytes`, which could only be
  uploaded compressed. A video over `max_seconds`, or still over `max_bytes` after the user's choice,
  is skipped with a message that names the limit.
- **Reading is open to any signed-in user** (like `GET /v1/me`): the limits are not private, and the
  app reads them for a group it belongs to. An unreadable object is `502`, never the defaults: that
  would lift a limit the owner set. A read is reused for 30 seconds; a write drops it at once.
