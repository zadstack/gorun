# Workspace audit improvements

This increment addresses audit items 2, 21–25, 27, 35 and 60. It changes presentation and owner-scoped read projections; existing approval, key creation, discovery and generation contracts remain authoritative.

- **Dashboard (2):** daily work precedes setup in the default order. An owner-only queue links canvases with managed-tool requests awaiting review. Personal saved section order remains intact. Reordering controls appear only after opening Customize dashboard; system details and workspace setup are collapsed.
- **Canvases (21–22):** saved boards have search and their last run's stored state. Starter choices show input/output guidance and setup requirements; personal structural templates appear as cards. An empty content reference asks for a selection, while a selected inaccessible reference retains its access warning. IDs, webhook URL and saved instructions sit inside a card disclosure, with current event state and setup action outside. Exact run review is unchanged.
- **Approvals (23):** the public MCP approvals screen explains its source and links the authorized canvas section for managed-tool review. Dashboard hints never approve calls; every exact managed call remains in its original canvas review.
- **Keys (24):** the key list comes first. Explicit creation and permission disclosures reduce the initial form. Narrow read-content and private-canvas presets select only permissions already available to that user; selecting a preset does not create a key or grant a board. The full permission list and wildcard option remain available for explicit review.
- **Agent drawer (25, 60):** the next action is prominent; repeated identity is reduced. Authorized bundle managers see selected skill names and saved MCP discovery states, never endpoints, credentials or resource text. Stored success is labelled as a past discovery rather than a live connection. Mobile spacing is reduced. Tab and Shift+Tab wrap inside the modal; Escape and delayed-refresh focus behavior remain covered.
- **Connections (27):** type leads the form with purpose and endpoint examples, without replacing a supplied endpoint. Existing type-specific fields, save/test/activate separation and version checks remain. The separate Metricool integration is collapsed.
- **Studio (35):** reusable instructions and tool bindings have structured fields, with cards for discovered tools and selectable exact tool names. Advanced JSON remains available for provider-specific argument objects and asynchronous status contracts. Editing and opening the page perform no discovery or execution; the existing explicit save/discover actions remain.

## Boundaries

This is not a merged approval authority or an automatic least-privilege inference engine. Starter cards describe their structural intent; they do not preview generated content or assert a configured provider. Arbitrary provider input schemas and asynchronous mappings still require advanced configuration; the UI does not invent an API contract. It does not claim a complete screen-reader audit: focused keyboard, RTL, mobile and delayed-response regressions are included.

Browser evidence uses an isolated temporary database and synthetic data. No running customer site is changed.
