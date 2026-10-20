# Extension installation methods

The Extensions directory separates installed records by the method recorded for
that installation: **Server directory**, **Uploaded ZIP**, or **Legacy: method
not recorded**. The same label appears on each detail page.

`InstallDir` records `directory`, even when the directory is outside GoRun's data
folder. `InstallZip` records `upload`. An operator's directory can contain a
previously uploaded package; reinstalling it through `InstallDir` records the new
installation method. Neither method proves package authorship, official origin,
signature verification or safety. Available packages are just packages currently
present in the server's extensions directory.

Migration `0002_install_source.sql` adds `install_source` with the conservative
`legacy_unknown` default. All pre-migration records, whether originally uploaded
or installed from a directory, remain unknown. No filesystem inspection or missing
metadata is used to guess their history. Existing secrets, keys, status, manifest,
subscription and delivery records are retained. Repeating migration is idempotent.

This field changes no installation/activation permissions, CSRF protection,
manifest validation, credential handling, rollback, hook execution or network
policy. Continue reviewing each package's permissions and destination explicitly.

This implementation addresses the installation-directory proposal in PR #82
(original commit `793f50218e94602274a1bc6503066bfc80e2e05e`, by Cursor Agent)
with a new authored repair on current main. The original ref is preserved;
no missing contributor sign-off is fabricated or retroactively attributed.
