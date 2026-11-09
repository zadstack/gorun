# Dashboard review — 2026-11-09

Scope: `/admin` only. Existing DESIGN.md, Zad font, palette and spacing tokens retained. No generated default design system replaces the project identity.

| Where | Before | Fix | Basis |
|---|---|---|---|
| setup_status.html | Expanded 11-item setup checklist dominates first viewport | Collapsed native details, explicit attention text when applicable | OpenDesigner audit §3.2 hierarchy; project DESIGN.md detail disclosure |
| dashboard grid | Four-column layout leaves content/analytics in narrow quarter-width panels | Two half-width panels; compact three-column operational summaries | OpenDesigner audit hierarchy; measured desktop screenshot |
| Table and secondary labels | 11px table headings and several sub-12px labels | Scoped 13px label, 15px body and 17px section-title tokens | OpenDesigner preflight 9 / audit §3.2 |
| Dashboard pins empty state | Large dashed drop target competes with working content | Compact text-aligned empty state; drop feedback preserved | Project detail disclosure; own design judgment |
| Empty content copy | Refers to actions “above” although personal order varies | Refers to Quick actions by name | OpenDesigner audit labels/clarity |
| Mobile account controls, 200% text | Logout row exceeds viewport | Wrap account actions on this dashboard | WCAG 1.4.4; measured browser overflow |

Tools: OpenDesigner detect, CSS extraction, browser probe, measured review and preflight; independent reviewer inspected RTL desktop/mobile screenshots. Reviewer requested explicit attention text; implemented using existing onboarding translation.
