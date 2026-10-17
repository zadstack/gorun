Test fixture: an installed custom theme loaded through GORUN_THEMES_EXTRA, the
way sites load themes kept outside the core `themes/` directory. It inherits
`default`, does not declare the identity capability, and ships one font and
one stylesheet for the asset and CORS tests.
