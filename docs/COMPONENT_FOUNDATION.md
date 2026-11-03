# Component foundation

The first component-library package adds task entry, result layouts and native
directory discovery to the page builder. Public detail, navigation, action and
feedback composition is documented in [PUBLIC_UI_COMPONENTS.md](PUBLIC_UI_COMPONENTS.md).
These are presentation capabilities; requests, checkout and membership still use
their own optional modules and server authorization.

## Task entry and result layouts

Choose **Task entry / مدخل المهمة** in the page builder. Its native layouts are
`search_first`, `choice_first` and `service_first`. Choice and service layouts need
at least one titled, linked item. Search requires a label and a root-relative
destination such as `/search` or `/services`; it accepts no external destination,
query string or fragment. The parameter defaults to `q` and must be a lowercase
identifier. This is a GET search destination, not an arbitrary form or command.

The editor stores a sanitized link marker. The theme validates and expands that
marker into a labeled GET search form at render time; the sanitizer still rejects
raw forms in content. If expansion is unavailable, the marker remains a useful
destination link. Known locale routes are localized through the existing router.

Native **Features / مميزات** also offers `compact_rows`, `image_led` and
`featured_secondary`. Text, links and images stay in structured entries; layouts
do not accept arbitrary HTML, class names or CSS. The featured layout uses the
first marked entry, or the first entry if none is marked. Existing default
layouts remain available.

The scoped native stylesheet is loaded only when the rendered body uses these
layouts. Both themed and body-only builder previews inline the same stylesheet
inside their existing read-only sandbox. Previews do not enable form submission.

## Directory discovery

A native directory selects an existing public content type and uses the shared
archive library for search, taxonomy/field filters, counts and pagination. It
shows only published, publicly readable content in the selected locale, with 24
records per page. Private or unavailable types render no directory; a query
failure produces an error and retry action rather than an empty-result claim.
Custom card component overrides retain precedence.

One directory uses the established `q`, `page` and filter query keys. Multiple
directories use `dir_<section-id>_<key>` to preserve each directory's independent
state. Reordering sections does not change this scope. Unrelated query values
are preserved when submitting or clearing one directory. An unscoped `q` remains
an initial fallback for old multi-directory URLs; an explicitly empty scoped
query overrides it.

## Theme adoption

Updating the core makes the builder layouts and rendering available. A custom
theme must actually render builder bodies or adopt the new public components to
show them. Installing the core alone does not rewrite its templates or existing
page layouts. The default theme opts into public UI; child themes declare their
own `public-ui` support explicitly. Site identity tokens remain the source of
colors and typography.

Review the rendered journey from task entry to search, selected result and return
on mobile and desktop, including RTL, keyboard focus, empty results, errors and
unavailable modules. A layout change is not a substitute for a working action.
