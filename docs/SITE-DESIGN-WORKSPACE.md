# Site design workspace

Website → Design contains one **Site design** entry. The saved site summary and two page tabs are shared by the existing Build site and Visual identity screens. The summary reads stored site settings (with the same default fallback for absent/invalid shared identity); edits in either form do not appear there until saved/applied.

`/admin/site-design` chooses Build site only when the signed-in user has all five existing permissions: `settings.manage`, `content.create`, `content.edit`, `content.publish`, and `menus.manage`. Settings-only users land on Visual identity and receive no Build tab. The existing `/admin/site-builder`, draft review query URLs and `/admin/appearance/identity` links continue to work. Permission checks on the original GET/POST routes are unchanged.

Build site retains its three-step design/profile/draft workflow, pending-page previews, expected-version activation check and explicit activation action. Creating draft pages does not change the current public homepage or theme. Visual identity keeps its separate POST validation and explicit Save action; its local sample does not save. Saving identity applies to the current site, and theme-specific overrides can still take precedence. Switching tabs never submits or activates either form. With JavaScript, edited forms ask before discarding changes on a workspace-tab switch and use the browser's standard unload warning for other navigation.

Validation errors stay on the form's selected workspace page and receive focus when JavaScript is available. A failed POST does not repeat a prior `?saved=1` success notice. Without JavaScript, both links and native forms work, all starter steps remain visible, and a narrowly scoped fallback prevents the legacy sticky admin sidebar from covering this workspace's forms.

The existing `site-builder` navigation ID is retained. The duplicate `builder.identity` registry entry is removed; an old identity deep link remains valid, but an old custom navigation pin for the removed duplicate is no longer offered. No customer identity is reset and no data migration is required.
