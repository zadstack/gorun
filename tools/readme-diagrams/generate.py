#!/usr/bin/env python3
"""Generate the README diagrams in English and Arabic.

Writes assets/branding/{approval-journey,architecture}{,-ar}.svg. The Arabic
files mirror the English layout so flows read right to left. Run from the
repository root:

    python3 tools/readme-diagrams/generate.py          # write the files
    python3 tools/readme-diagrams/generate.py --check  # fail if they drifted
"""

import re
import sys
from pathlib import Path
from xml.sax.saxutils import escape

OUT = Path("assets/branding")
ARABIC = re.compile("[؀-ۿ]")
W = 1000

INK, MUTED, ACCENT, LINE = "#080e14", "#545e69", "#1748ed", "#dce2e8"
BG, CARD, ACCENT_BG = "#f7f9fc", "#fff", "#eef3ff"
GREEN, GREEN_BG = "#167252", "#edf7f1"
RED, RED_BG = "#b42318", "#fdf0ef"
AMBER, AMBER_BG = "#8a5a00", "#fff6e5"
WIRE = "#aebed7"

FONT_EN = '-apple-system, BlinkMacSystemFont, "Segoe UI", Arial, sans-serif'
FONT_AR = 'Tahoma, "Segoe UI", Arial, sans-serif'
MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'


class Canvas:
    def __init__(self, lang, height, title, desc):
        self.ar = lang == "ar"
        self.h = height
        self.parts = []
        self.title, self.desc = title, desc

    def x(self, x, w=0):
        """Mirror a left edge (and width) for right-to-left layouts."""
        return W - x - w if self.ar else x

    def rect(self, x, y, w, h, fill=CARD, stroke=LINE, dash=False, sw=1):
        d = ' stroke-dasharray="6 5"' if dash else ""
        self.parts.append(
            f'<rect x="{self.x(x, w)}" y="{y}" width="{w}" height="{h}" rx="10" '
            f'fill="{fill}" stroke="{stroke}" stroke-width="{sw}"{d}/>')

    def text(self, x, y, s, size=13, weight=400, color=MUTED, anchor="middle",
             mono=False):
        x = self.x(x)
        fam = f' font-family="{MONO}"' if mono else ""
        # Latin-only labels (paths, module names, "SQLite (WAL)") must stay
        # left to right inside the Arabic diagrams, or "/admin" reads "admin/".
        rtl = ""
        if self.ar:
            if ARABIC.search(s) and not mono:
                rtl = ' direction="rtl"'
            else:
                rtl = ' direction="ltr"'
                # A mirrored left edge is now a right edge for LTR text.
                if anchor == "start":
                    anchor = "end"
        self.parts.append(
            f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" '
            f'fill="{color}" text-anchor="{anchor}"{fam}{rtl}>{escape(s)}</text>')

    def path(self, d_pts, color=ACCENT, arrow=True, sw=2):
        """Polyline through points; arrowhead at the last point."""
        pts = [(self.x(px), py) for px, py in d_pts]
        d = "M" + " L".join(f"{px} {py}" for px, py in pts)
        m = ' marker-end="url(#arrow)"' if arrow else ""
        self.parts.append(
            f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{sw}" '
            f'stroke-linecap="round" stroke-linejoin="round"{m}/>')

    def card(self, x, y, w, h, label, head, lines=(), fill=CARD, stroke=LINE,
             dash=False, label_color=None, mono_lines=()):
        self.rect(x, y, w, h, fill, stroke, dash)
        cx = x + w / 2
        self.text(cx, y + 24, label, 11, 700, label_color or MUTED)
        self.text(cx, y + 48, head, 17, 650, INK)
        for i, ln in enumerate(lines):
            self.text(cx, y + 70 + i * 18, ln, 12.5, 400, MUTED,
                      mono=i in mono_lines)

    def svg(self):
        lang = "ar" if self.ar else "en"
        font = FONT_AR if self.ar else FONT_EN
        rtl = ' direction="rtl"' if self.ar else ""
        head = (
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {self.h}" '
            f'width="{W}" height="{self.h}" role="img" aria-labelledby="title desc" '
            f'lang="{lang}"{rtl}>\n'
            f'<title id="title">{escape(self.title)}</title>'
            f'<desc id="desc">{escape(self.desc)}</desc>\n'
            f'<style>text {{ font-family: {font}; }}</style>\n'
            '<defs><marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" '
            'markerWidth="7" markerHeight="7" orient="auto-start-reverse">'
            f'<path d="M1 1 L9 5 L1 9" fill="none" stroke="{ACCENT}" '
            'stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>'
            '</marker></defs>\n'
            f'<rect x=".5" y=".5" width="{W - 1}" height="{self.h - 1}" rx="14" '
            f'fill="{BG}" stroke="{LINE}"/>\n')
        return head + "\n".join(self.parts) + "\n</svg>\n"


APPROVAL = {
    "en": {
        "title": "How an agent action gets approved",
        "desc": ("An AI agent calls an MCP tool. GoRun checks the key's scopes and the "
                 "arguments. A tool that is not sensitive runs at once. A sensitive tool is "
                 "parked as pending and expires after 24 hours. A person reviews it under "
                 "Canvas & agents, Approvals. Approval runs it once as the requesting key; denial "
                 "or expiry runs nothing. The agent reads the outcome with check_approval, "
                 "and every step is written to the audit log."),
        "sub": "Sensitive MCP tools wait for a person. Nothing runs twice, and every step is audited.",
        "c1": ("01 · AGENT", "Calls an MCP tool", ["With an AI agent key", "Scopes stay within owner's role"]),
        "c2": ("02 · GORUN", "Checks the call", ["Key scopes cover the tool", "Arguments match the schema"]),
        "c3": ("03 · RULE", "Sensitive tool?", ["Publish, delete, live edits,", "themes, settings"]),
        "c4": ("RESULT", "Runs now", ["Result goes to the agent", "Also with mcp.approve_self"]),
        "no": "No", "yes": "Yes",
        "c5": ("04 · WAITS", "Parked for review", ["Returns pending_approval", "Expires after 24 hours"]),
        "c6": ("05 · A PERSON", "Reviews and decides", ["Canvas & agents → Approvals", "Tool, arguments, key, requester"]),
        "rule": ("Who may decide", ["A key never decides its own call.",
                                    "Over REST, other keys of the same owner",
                                    "cannot decide either, unless the site opts in."]),
        "o1": ("06 · AGENT", "Reads the outcome", ["check_approval returns", "the status and result"]),
        "o2": ("APPROVE", "Runs once", ["As the requesting key, if it", "is still active and in scope"]),
        "o3": ("DENY", "Nothing runs", ["Status: denied"]),
        "o4": ("NO DECISION", "Expires", ["After 24 hours", "Status: expired"]),
        "audit": "Audit log",
        "audit1": "Every call: mcp.call with a SHA-256 of the arguments, never the arguments themselves.",
        "audit2": "Every decision: mcp.approval.approve or deny. Approved runs record who approved them.",
    },
    "ar": {
        "title": "كيف يُعتمد إجراء الوكيل",
        "desc": ("يستدعي وكيل ذكاء اصطناعي أداة MCP، فيتحقق GoRun من صلاحيات المفتاح والوسائط. "
                 "تُنفَّذ الأداة غير الحساسة فورًا، وتُحفَظ الأداة الحساسة بانتظار المراجعة "
                 "وتنتهي صلاحيتها بعد 24 ساعة. يراجعها شخص من الكانفس والوكلاء ← الموافقات. الموافقة تنفذها "
                 "مرة واحدة بمفتاح الطالب، والرفض أو انتهاء المهلة لا ينفذ شيئًا. يقرأ الوكيل "
                 "النتيجة بالأداة check_approval، وتُسجَّل كل خطوة في سجل التدقيق."),
        "sub": "أدوات MCP الحساسة تنتظر قرار شخص. لا يُنفَّذ شيء مرتين، وتُسجَّل كل خطوة.",
        "c1": ("01 · الوكيل", "يستدعي أداة MCP", ["بمفتاح وكيل ذكاء اصطناعي", "صلاحياته ضمن دور مالكه"]),
        "c2": ("02 · GoRun", "يتحقق من الاستدعاء", ["صلاحيات المفتاح تغطي الأداة", "الوسائط مطابقة للمخطط"]),
        "c3": ("03 · القاعدة", "هل الأداة حساسة؟", ["النشر والحذف وتعديل المنشور", "والقوالب والإعدادات"]),
        "c4": ("النتيجة", "يُنفَّذ فورًا", ["تعود النتيجة إلى الوكيل", "وكذلك مع mcp.approve_self"]),
        "no": "لا", "yes": "نعم",
        "c5": ("04 · الانتظار", "يُحفَظ للمراجعة", ["يُرجع pending_approval", "تنتهي مهلته بعد 24 ساعة"]),
        "c6": ("05 · شخص", "يراجع ويقرر", ["الكانفس والوكلاء ← الموافقات", "الأداة والوسائط والمفتاح والطالب"]),
        "rule": ("من يحق له القرار", ["لا يقرر أي مفتاح في استدعائه أبدًا.",
                                      "وعبر REST لا تقرر مفاتيح المالك نفسه أيضًا،",
                                      "إلا إذا فعّل الموقع ذلك صراحة."]),
        "o1": ("06 · الوكيل", "يقرأ النتيجة", ["الأداة check_approval تُرجع", "الحالة والنتيجة"]),
        "o2": ("موافقة", "يُنفَّذ مرة واحدة", ["بمفتاح الطالب، إن ظل فعّالًا", "وصلاحياته تغطي الأداة"]),
        "o3": ("رفض", "لا يُنفَّذ شيء", ["الحالة: denied"]),
        "o4": ("بلا قرار", "تنتهي المهلة", ["بعد 24 ساعة", "الحالة: expired"]),
        "audit": "سجل التدقيق",
        "audit1": "كل استدعاء: mcp.call مع بصمة SHA-256 للوسائط، لا الوسائط نفسها.",
        "audit2": "كل قرار: mcp.approval.approve أو deny، ويُسجَّل اسم من وافق على كل تنفيذ معتمد.",
    },
}


def approval(lang):
    t = APPROVAL[lang]
    c = Canvas(lang, 668, t["title"], t["desc"])
    anchor = "start"
    c.text(32, 46, t["title"], 24, 700, INK, anchor)
    c.text(32, 74, t["sub"], 14.5, 400, MUTED, anchor)

    y1, y2, y3, h = 104, 262, 420, 104
    c.card(32, y1, 210, h, *t["c1"])
    c.card(272, y1, 210, h, *t["c2"])
    c.card(512, y1, 210, h, *t["c3"], fill=ACCENT_BG, stroke=ACCENT, label_color=ACCENT)
    c.card(752, y1, 216, h, *t["c4"], fill=GREEN_BG, stroke=GREEN, label_color=GREEN)
    c.path([(244, 156), (268, 156)])
    c.path([(484, 156), (508, 156)])
    c.path([(724, 156), (748, 156)])
    c.text(736, 146, t["no"], 12, 700, ACCENT)
    c.path([(617, 210), (617, 258)])
    c.text(633 if lang == "en" else 601, 240, t["yes"], 12, 700, ACCENT)

    c.card(512, y2, 210, h, *t["c5"], fill=AMBER_BG, stroke=AMBER, label_color=AMBER)
    c.card(752, y2, 216, h, *t["c6"])
    c.path([(724, 314), (748, 314)])
    rl, rlines = t["rule"]
    c.rect(32, y2, 450, h, CARD, WIRE, dash=True)
    c.text(52, y2 + 30, rl, 15, 650, INK, anchor)
    for i, ln in enumerate(rlines):
        c.text(52, y2 + 54 + i * 19, ln, 13, 400, MUTED, anchor)

    # From the decision to the three outcomes.
    c.path([(860, 368), (860, 392), (377, 392), (377, 416)], WIRE, arrow=False)
    c.path([(377, 392), (377, 416)])
    c.path([(617, 392), (617, 416)])
    c.path([(860, 392), (860, 416)])
    hh = 96
    c.card(32, y3, 210, hh, *t["o1"], label_color=ACCENT)
    c.card(272, y3, 210, hh, *t["o2"], fill=GREEN_BG, stroke=GREEN, label_color=GREEN)
    c.card(512, y3, 210, hh, *t["o3"], fill=RED_BG, stroke=RED, label_color=RED)
    c.card(752, y3, 216, hh, *t["o4"], label_color=MUTED)
    # Every outcome reaches the agent.
    c.path([(377, 516), (377, 540)], WIRE, arrow=False)
    c.path([(617, 516), (617, 540)], WIRE, arrow=False)
    c.path([(860, 516), (860, 540), (137, 540), (137, 520)])

    c.rect(32, 566, 936, 80, CARD, LINE)
    c.text(52, 596, t["audit"], 15, 700, INK, anchor)
    c.text(52, 620, t["audit1"], 13, 400, MUTED, anchor)
    c.text(52, 638, t["audit2"], 13, 400, MUTED, anchor)
    return c.svg()


ARCH = {
    "en": {
        "title": "How GoRun fits together",
        "desc": ("Visitors, staff, integrations, AI agents and providers reach one gorun "
                 "process through the public site, the admin, REST, MCP and webhooks. Inside, "
                 "modules boot in a fixed order on shared kernel services: hooks, a durable "
                 "jobs queue, the audit log, settings and translations. Data lives in one "
                 "SQLite database and a data directory; themes, patterns and extensions load "
                 "from folders; external providers are optional."),
        "sub": "One Go binary and one SQLite database. Modules boot in a fixed order on shared kernel services.",
        "left": "WHO CONNECTS", "mid": "ONE GORUN PROCESS", "right": "DATA AND OUTSIDE",
        "clients": [("Visitors", "Public site, themes"), ("Staff", "Admin in Arabic/English"),
                    ("Integrations", "REST with API keys"), ("AI agents", "MCP with agent keys"),
                    ("Providers", "Signed webhooks in")],
        "routes": [("/", "Public"), ("/admin", "Admin"), ("/api/v1", "REST + OpenAPI"),
                   ("/mcp", "MCP + approvals"), ("/webhooks", "Webhooks")],
        "mods": "MODULES · FIXED BOOT ORDER",
        "groups": [("CONTENT", ["settings · auth · content · taxonomy", "media · menus · redirects · seo"]),
                   ("SITE", ["theme · pattern · builder · site"]),
                   ("INTERFACES", ["api · admin · webhooks · mcp · ext"]),
                   ("ADD-ONS", ["commerce · saas · membership · launch",
                                "workagents · identityai · identitylogo", "wpmigrate"])],
        "kernel": "KERNEL",
        "kernel_lines": ["Services, hooks, durable jobs and cron", "Audit log, settings, Arabic/English, RTL",
                         "Background reads, staged updates"],
        "stores": [("SQLite (WAL)", ["Content, users, settings,", "audit log and jobs"]),
                   ("Data directory", ["Media, form uploads", "and backups"]),
                   ("Themes, patterns", ["Folders loaded at boot"]),
                   ("Extensions", ["Manifests, HTTP hooks"]),
                   ("Optional providers", ["AI, Stripe, SMTP mail,", "Cloudflare Edge"])],
        "legend": ("Dashed: optional. Add-on modules, mcp and ext switch on or off in Operations → Modules; "
                   "their data stays."),
    },
    "ar": {
        "title": "كيف تترابط أجزاء GoRun",
        "desc": ("يصل الزوار والفريق والتكاملات ووكلاء الذكاء الاصطناعي والمزودون إلى عملية gorun "
                 "واحدة عبر الموقع العام ولوحة الإدارة وREST وMCP والويب هوك. في الداخل تُقلع "
                 "الوحدات بترتيب ثابت فوق خدمات نواة مشتركة: الخطافات وطابور المهام وسجل التدقيق "
                 "والإعدادات والترجمة. تُحفظ البيانات في قاعدة SQLite واحدة ومجلد بيانات، وتُحمَّل "
                 "القوالب والأنماط والإضافات من مجلدات، والمزودون الخارجيون اختياريون."),
        "sub": "ملف Go تنفيذي واحد وقاعدة SQLite واحدة. تُقلع الوحدات بترتيب ثابت فوق خدمات نواة مشتركة.",
        "left": "من يتصل", "mid": "عملية gorun واحدة", "right": "البيانات والخارج",
        "clients": [("الزوار", "الموقع العام والقوالب"), ("الفريق", "لوحة إدارة عربية وإنجليزية"),
                    ("التكاملات", "REST بمفاتيح API"), ("وكلاء الذكاء", "MCP بمفاتيح وكلاء"),
                    ("المزودون", "ويب هوك موقّع وارد")],
        "routes": [("/", "الموقع العام"), ("/admin", "الإدارة"), ("/api/v1", "REST وOpenAPI"),
                   ("/mcp", "MCP والموافقات"), ("/webhooks", "الويب هوك")],
        "mods": "الوحدات · ترتيب إقلاع ثابت",
        "groups": [("المحتوى", ["settings · auth · content · taxonomy", "media · menus · redirects · seo"]),
                   ("الموقع", ["theme · pattern · builder · site"]),
                   ("الواجهات", ["api · admin · webhooks · mcp · ext"]),
                   ("الإضافات", ["commerce · saas · membership · launch",
                                 "workagents · identityai · identitylogo", "wpmigrate"])],
        "kernel": "النواة",
        "kernel_lines": ["خدمات وخطافات وطابور مهام دائم وجدولة", "سجل تدقيق وإعدادات وعربية وإنجليزية وRTL",
                         "قراءات في الخلفية وتحديثات مرحلية"],
        "stores": [("SQLite (WAL)", ["المحتوى والمستخدمون والإعدادات", "وسجل التدقيق والمهام"]),
                   ("مجلد البيانات", ["الوسائط والمرفقات", "والنسخ الاحتياطية"]),
                   ("القوالب والأنماط", ["مجلدات تُحمَّل عند الإقلاع"]),
                   ("الإضافات", ["ملفات تعريف وخطافات HTTP"]),
                   ("مزودون اختياريون", ["ذكاء اصطناعي وStripe وبريد SMTP", "وCloudflare Edge"])],
        "legend": ("الحدود المتقطعة: اختياري. تُشغَّل الإضافات وmcp وext أو تُوقف من تشغيل النظام ← الوحدات، "
                   "وتبقى بياناتها."),
    },
}


def architecture(lang):
    t = ARCH[lang]
    c = Canvas(lang, 600, t["title"], t["desc"])
    anchor = "start"
    c.text(32, 46, t["title"], 24, 700, INK, anchor)
    c.text(32, 74, t["sub"], 14.5, 400, MUTED, anchor)

    c.text(32, 108, t["left"], 11, 700, MUTED, anchor)
    c.text(262, 108, t["mid"], 11, 700, ACCENT, anchor)
    c.text(788, 108, t["right"], 11, 700, MUTED, anchor)

    rows = [124 + 76 * i for i in range(5)]
    c.rect(262, 116, 476, 392, CARD, ACCENT, sw=1.5)
    for (name, sub), (route, rsub), y in zip(t["clients"], t["routes"], rows):
        c.rect(32, y, 180, 60, CARD, LINE)
        c.text(122, y + 26, name, 15, 650, INK)
        c.text(122, y + 46, sub, 12, 400, MUTED)
        c.path([(214, y + 30), (274, y + 30)])
        c.rect(278, y, 116, 60, ACCENT_BG, ACCENT_BG)
        c.text(336, y + 27, route, 14, 700, ACCENT, mono=True)
        c.text(336, y + 46, rsub, 11.5, 400, MUTED)

    c.text(414, 140, t["mods"], 11, 700, MUTED, anchor)
    y = 150
    for label, lines in t["groups"]:
        h = 26 + 17 * len(lines)
        optional = label in ("ADD-ONS", "الإضافات")
        c.rect(410, y, 312, h, CARD if optional else BG, WIRE if optional else LINE, dash=optional)
        c.text(424, y + 19, label, 10.5, 700, ACCENT, anchor)
        for i, ln in enumerate(lines):
            c.text(566, y + 38 + 17 * i, ln, 12, 400, INK, mono=True)
        y += h + 6
    ky = 408
    c.rect(410, ky, 312, 88, ACCENT_BG, ACCENT_BG)
    c.text(424, ky + 20, t["kernel"], 10.5, 700, ACCENT, anchor)
    for i, ln in enumerate(t["kernel_lines"]):
        c.text(566, ky + 41 + 18 * i, ln, 12.5, 400, INK)

    sy = 124
    for i, (name, lines) in enumerate(t["stores"]):
        h = 44 + 18 * len(lines) - (8 if len(lines) == 1 else 0)
        optional = i == len(t["stores"]) - 1
        c.rect(788, sy, 180, h, CARD, WIRE if optional else LINE, dash=optional)
        c.text(878, sy + 25, name, 14.5, 650, INK)
        for j, ln in enumerate(lines):
            c.text(878, sy + 45 + 17 * j, ln, 12, 400, MUTED)
        c.path([(740, sy + h / 2), (784, sy + h / 2)], WIRE if optional else ACCENT)
        sy += h + 10

    c.text(32, 548, t["legend"], 13, 400, MUTED, anchor)
    c.text(32, 572, "github.com/zadstack/gorun · docs/ARCHITECTURE.md", 12, 400, WIRE, anchor, mono=True)
    return c.svg()


def main():
    check = "--check" in sys.argv[1:]
    files = {}
    for lang, suffix in (("en", ""), ("ar", "-ar")):
        files[f"approval-journey{suffix}.svg"] = approval(lang)
        files[f"architecture{suffix}.svg"] = architecture(lang)
    stale = []
    for name, body in files.items():
        p = OUT / name
        if check:
            if not p.exists() or p.read_text(encoding="utf-8") != body:
                stale.append(name)
        else:
            p.write_text(body, encoding="utf-8")
    if stale:
        print("stale diagrams, run python3 tools/readme-diagrams/generate.py:", ", ".join(stale))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
