# Personal quick access

Use **＋ Add to quick access** beside a page in the top contextual tabs. The shortcut appears immediately in **Quick access**, below the main sidebar sections and above Customize sidebar. The current page stays open, and the sidebar retains its scroll position. On mobile, saving never opens the sidebar drawer automatically.

Quick access uses the existing personal dashboard pins. Previously saved pins appear without a migration. The dashboard pinned-widget view still works with the same records, ordering and removal controls; a successful change refreshes both views when the widget is on the current page. The page-tab button shows its saved state and can remove the shortcut. Each sidebar shortcut also has a separately labelled remove button. Removing a shortcut does not delete its underlying content or tool.

Only currently authorized registry destinations are projected. Workspace mode and current permissions apply on initial rendering and every refresh. A revoked permission or Simple workspace can hide a saved shortcut without deleting its placement. Another user's pins, labels or record data are never included. Pinning does not grant access or create a new permission. Existing CSRF, per-user storage, idempotent add and transactional ordering/removal remain in use.

The UI refreshes saved state after successful mutations and on a persisted browser-back restoration. Pending requests are aborted on leaving; stale responses are discarded, controls recover after cache restoration, and an authoritative read determines the displayed state. An uncertain save is not retried automatically. Native links remain ordinary authorized navigation links.

Legacy `builder.identity` pins are read as the current authorized `site-builder` registry destination (now Site design). Both stored IDs are deduplicated in the projection. GET does not rewrite pins or navigation preferences. Explicit add is idempotent across the family; explicit order updates existing rows without inserting aliases. Explicit removal removes both of the current user's alias rows and restores only those two logical visibility IDs, preserving unrelated hidden items and saved item/group orders.

The read-only `GET /admin/dashboard/pins` projection accepts an optional relative admin `page` path for current-link styling, plus `widget=1` to refresh the existing widget. It performs no provider probes, dispatches, preference writes or remote fetches. POST routes and storage are unchanged.
