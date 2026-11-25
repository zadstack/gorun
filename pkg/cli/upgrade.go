package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/update"
)

const imageUpgradeDocs = upgradeRepository + "/blob/main/docs/UPGRADE.md#3-upgrade--docker--ghcr-and-coolify"

type binaryUpgrade struct {
	releases binaryReleases
	path     string
	current  string
	platform string
	out      io.Writer
}

type previousBinary struct {
	ThemesDir      string            `json:"themes_dir,omitempty"`
	Themes         []previousTheme   `json:"themes,omitempty"`
	Version        string            `json:"version"`
	BackupStamp    string            `json:"backup_stamp"`
	DataDir        string            `json:"data_dir"`
	PreviousSHA256 [sha256.Size]byte `json:"previous_sha256"`
	ActiveSHA256   [sha256.Size]byte `json:"active_sha256"`
}

func upgradeCmd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(out)
	to := fs.String("to", "", "target release tag (vX.Y.Z)")
	yes := fs.Bool("yes", false, "allow replacement while the site is running")
	binaryOnly := fs.Bool("binary-only", false, "replace only the executable and warn about skipped themes")
	dry := fs.Bool("dry-run", false, "print the upgrade plan without writing files")
	data := fs.String("data", "", "site data directory (defaults to GORUN_DATA or ./data)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("upgrade: unexpected positional argument")
	}
	var cfgArgs []string
	var explicit bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "data":
			cfgArgs = []string{"--data", *data}
		case "to":
			explicit = true
		}
	})
	if *data == "" && len(cfgArgs) != 0 {
		return errors.New("upgrade: --data needs a directory")
	}
	if explicit && *to == "" {
		return errors.New("upgrade: --to needs a release tag")
	}
	u, err := currentBinaryUpgrade(out)
	if err != nil {
		return err
	}
	cfg, err := config.LoadReadOnly(cfgArgs)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return u.upgrade(ctx, cfg, *to, *yes, *dry, *binaryOnly)
}

func rollbackCmd(args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, "Usage: gorun rollback")
		return nil
	}
	if len(args) != 0 {
		return errors.New("usage: gorun rollback")
	}
	u, err := currentBinaryUpgrade(out)
	if err != nil {
		return err
	}
	return u.rollback()
}

func currentBinaryUpgrade(out io.Writer) (binaryUpgrade, error) {
	if err := checkBinaryInstall(runtime.GOOS, runtime.GOARCH, containerInstall(os.Stat, os.ReadFile, os.Getenv)); err != nil {
		return binaryUpgrade{}, err
	}
	path, err := installedBinary()
	if err != nil {
		return binaryUpgrade{}, fmt.Errorf("locate installed binary: %w", err)
	}
	return binaryUpgrade{officialBinaryReleases(), path, version, runtime.GOOS + "-" + runtime.GOARCH, out}, nil
}

func checkBinaryInstall(goos, arch string, container bool) error {
	if container {
		return fmt.Errorf("image deployment detected; follow the Docker/Coolify upgrade runbook: %s", imageUpgradeDocs)
	}
	if (goos != "linux" && goos != "darwin") || (arch != "amd64" && arch != "arm64") {
		return fmt.Errorf("binary updates are unavailable on %s/%s", goos, arch)
	}
	return nil
}

func containerInstall(stat func(string) (os.FileInfo, error), read func(string) ([]byte, error), getenv func(string) string) bool {
	if getenv("COOLIFY_RESOURCE_UUID") != "" || getenv("container") != "" {
		return true
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv", "/run/systemd/container"} {
		if _, err := stat(marker); err == nil {
			return true
		}
	}
	for _, path := range []string{"/proc/1/cgroup", "/proc/self/cgroup", "/proc/self/mountinfo"} {
		data, _ := read(path)
		for _, marker := range []string{"/docker/", "/docker-", "/kubepods", "/libpod-", "/containers/storage/", "/lxc/"} {
			if strings.Contains(string(data), marker) {
				return true
			}
		}
	}
	return false
}

const stopUpgradeService = "Stop GoRun before replacing the binary (systemd: sudo systemctl stop gorun; foreground: Ctrl+C)."
const restartUpgradeService = "Start GoRun with the same service configuration (systemd: sudo systemctl start gorun; foreground: rerun your start command)."

func (u binaryUpgrade) upgrade(ctx context.Context, cfg config.Config, target string, yes, dry, binaryOnly bool) (result error) {
	current := "v" + strings.TrimPrefix(u.current, "v")
	explicit := target != ""
	if explicit && !upgradeTag.MatchString(target) {
		return errors.New("upgrade: --to requires a release tag of the form vX.Y.Z")
	}
	if !upgradeTag.MatchString(current) {
		return errors.New("upgrade: the running binary must have a release version (vX.Y.Z)")
	}
	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return err
	}
	cfg.DataDir = dataDir
	if st, err := os.Stat(cfg.DBPath()); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("upgrade: no site database at %s; use --data or GORUN_DATA to select the existing site", cfg.DBPath())
	}
	held, err := kernel.DataDirLockHeld(dataDir)
	if err != nil {
		return err
	}
	if held && !yes && !dry {
		return upgradeLockedError()
	}
	if !explicit {
		target, err = u.releases.latest(ctx)
		if err != nil {
			return err
		}
	}
	if target == current {
		fmt.Fprintf(u.out, "Already running release %s.\n", current)
		return nil
	}
	if !explicit && !update.Newer(target, current) {
		return fmt.Errorf("upgrade: refusing downgrade from %s to %s without --to", current, target)
	}

	themeDir, err := filepath.Abs(theme.ThemesDir(cfg))
	if err != nil && !binaryOnly {
		return themeUpgradeError(theme.ThemesDir(cfg), err)
	}
	if themeDir == "" {
		themeDir = theme.ThemesDir(cfg)
	}
	if dry {
		if !binaryOnly {
			resolved, err := filepath.EvalSymlinks(themeDir)
			if err != nil {
				return themeUpgradeError(themeDir, err)
			}
			if err := checkThemeDirMode(resolved); err != nil {
				return themeUpgradeError(themeDir, err)
			}
			themeDir = resolved
		}
		_, slugs, err := u.releases.stage(ctx, "", "", target, u.platform)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", target, err)
		}
		if binaryOnly {
			warnSkippedThemes(u.out, themeDir, slugs)
		} else if err := reportUpgradeThemes(u.out, themeDir, slugs); err != nil {
			return err
		}

		fmt.Fprintf(u.out, "Upgrade %s to %s (%s) at %s.\n", current, target, u.platform, u.path)
		fmt.Fprintln(u.out, stopUpgradeService)
		fmt.Fprintln(u.out, "Download the release archive and verify its SHA-256. Stage and check the new executable beside the installed binary.")
		fmt.Fprintf(u.out, "Create and verify a backup in %s and print its stamp.\n", filepath.Join(dataDir, "backups"))
		fmt.Fprintf(u.out, "Save %s.previous and its version and backup stamp in %s.previous.json. Atomically replace the installed executable.\n", u.path, u.path)
		fmt.Fprintln(u.out, restartUpgradeService)
		if held && !yes {
			return upgradeLockedError()
		}
		if held {
			fmt.Fprintln(u.out, "--yes would allow replacement while GoRun is running.")
		}
		return nil
	}

	dir := filepath.Dir(u.path)
	lock, err := lockUpgradeDirectory(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Check both destinations before changing either rollback file.
	for _, path := range []string{u.path + ".previous", u.path + ".previous.json"} {
		st, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("upgrade: rollback destination %s must be a regular file", path)
		}
	}
	if err := verifyBinaryVersion(ctx, u.path, current); err != nil {
		return fmt.Errorf("check installed binary: %w", err)
	}
	var themeStage string
	keepThemeStage := false
	if !binaryOnly {
		var err error
		resolved, err := filepath.EvalSymlinks(themeDir)
		if err != nil {
			return themeUpgradeError(themeDir, err)
		}
		themeDir = resolved
		if themeDir != dir {
			themeLock, err := lockUpgradeDirectory(themeDir)
			if err != nil {
				return themeUpgradeError(themeDir, err)
			}
			defer themeLock.Close()
		}
		if err := checkThemeDirMode(themeDir); err != nil {
			return themeUpgradeError(themeDir, err)
		}
		themeStage, err = os.MkdirTemp(filepath.Dir(themeDir), ".gorun-themes-*")
		if err != nil {
			return themeUpgradeError(themeDir, err)
		}
		defer func() {
			if !keepThemeStage {
				os.RemoveAll(themeStage)
			}
		}()
		if err := checkThemeStaging(themeDir, themeStage); err != nil {
			return themeUpgradeError(themeDir, err)
		}
	}
	staged, slugs, err := u.releases.stage(ctx, dir, themeStage, target, u.platform)
	if err != nil {
		return fmt.Errorf("stage %s: %w", target, err)
	}
	defer os.Remove(staged)
	if err := verifyBinaryVersion(ctx, staged, target); err != nil {
		return fmt.Errorf("check staged binary: %w", err)
	}
	var plan []previousTheme
	if binaryOnly {
		warnSkippedThemes(u.out, themeDir, slugs)
	} else {
		plan, err = planThemeUpgrade(themeDir, slugs)
		if err == nil && len(plan) != 0 {
			err = checkPreviousThemes(themeDir, plan)
		}
		if err != nil {
			return themeUpgradeError(themeDir, err)
		}
	}

	dataLock, err := kernel.LockDataDir(dataDir)
	switch {
	case err == nil:
		defer dataLock.Release()
	case !errors.Is(err, kernel.ErrDataDirLocked):
		return err
	case !yes:
		return upgradeLockedError()
	default:
		fmt.Fprintln(u.out, "Continuing with --yes while GoRun is running. The running process stays on the old version until you restart it. Writes after the backup can be lost if you restore it.")
		fmt.Fprintln(u.out, stopUpgradeService)
	}

	backup, err := createVerifiedCLIBackup(cfg, false, false, u.out)
	if err != nil {
		return fmt.Errorf("pre-upgrade backup: %w", err)
	}
	fmt.Fprintf(u.out, "Backup stamp: %s\n", backup.Stamp)
	if err := ctx.Err(); err != nil {
		return err
	}
	meta := previousBinary{Version: current, BackupStamp: backup.Stamp, DataDir: dataDir}
	var installed bool
	if len(plan) != 0 {
		meta.ThemesDir, meta.Themes = themeDir, plan
		parent, err := os.OpenRoot(filepath.Dir(themeDir))
		if err != nil {
			return themeUpgradeError(themeDir, err)
		}
		defer parent.Close()
		renames := themeRenames{root: parent}
		previous := filepath.Join(filepath.Base(themeDir), ".previous")
		created := false
		for _, entry := range plan {
			if !entry.Added {
				err := parent.Mkdir(previous, 0o755)
				if err != nil && !errors.Is(err, os.ErrExist) {
					return themeUpgradeError(themeDir, err)
				}
				created = err == nil
				break
			}
		}
		defer func() {
			if installed {
				return
			}
			if err := renames.restore(); err != nil {
				keepThemeStage = true
				result = errors.Join(result, fmt.Errorf("theme recovery files at %s: %w", themeStage, err))
			} else if created {
				result = errors.Join(result, parent.Remove(previous))
			}
		}()
		if err := swapUpgradeThemes(&renames, filepath.Base(themeDir), filepath.Base(themeStage), plan); err != nil {
			return themeUpgradeError(themeDir, err)
		}
	}
	installed, err = u.install(lock, staged, meta)
	if err != nil {
		return err
	}

	fmt.Fprintf(u.out, "Installed %s. Previous binary: %s.previous\n", target, u.path)
	fmt.Fprintln(u.out, restartUpgradeService)
	return nil
}

func upgradeLockedError() error {
	return fmt.Errorf("data directory is locked. %s %s", stopUpgradeService, restartUpgradeService)
}

func verifyBinaryVersion(ctx context.Context, path, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return err
	}
	got, ok := strings.CutPrefix(strings.TrimSpace(string(output)), "GoRun ")
	if !ok || "v"+strings.TrimPrefix(got, "v") != tag {
		return fmt.Errorf("expected executable version %s", tag)
	}
	return nil
}

func (u binaryUpgrade) install(dir *os.File, staged string, meta previousBinary) (bool, error) {
	var err error
	meta.PreviousSHA256, err = binaryDigest(u.path)
	if err != nil {
		return false, err
	}
	meta.ActiveSHA256, err = binaryDigest(staged)
	if err != nil {
		return false, err
	}
	record, err := stageUpgradeFile(dir.Name(), 0o600, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(meta)
	})
	if err != nil {
		return false, err
	}
	defer os.Remove(record)
	link := record + ".previous"
	if err := os.Link(u.path, link); err != nil {
		return false, fmt.Errorf("preserve installed executable: %w", err)
	}
	defer os.Remove(link)
	if err := os.Rename(link, u.path+".previous"); err != nil {
		return false, err
	}
	if err := os.Rename(record, u.path+".previous.json"); err != nil {
		return false, err
	}
	if err := dir.Sync(); err != nil {
		return false, err
	}
	if err := os.Rename(staged, u.path); err != nil {
		return false, err
	}
	if err := dir.Sync(); err != nil {
		return true, fmt.Errorf("binary replaced but could not sync its directory: %w", err)
	}
	return true, nil
}

func (u binaryUpgrade) rollback() (result error) {
	dir, err := lockUpgradeDirectory(filepath.Dir(u.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	previous := u.path + ".previous"
	record := previous + ".json"
	raw, err := os.ReadFile(record)
	if err != nil {
		return fmt.Errorf("read rollback metadata: %w", err)
	}
	var meta previousBinary
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("read rollback metadata: %w", err)
	}
	if _, err := time.Parse("20060102T150405Z", meta.BackupStamp); err != nil || !upgradeTag.MatchString(meta.Version) || !filepath.IsAbs(meta.DataDir) {
		return errors.New("invalid rollback metadata")
	}
	for _, check := range []struct {
		path string
		want [sha256.Size]byte
	}{{u.path, meta.ActiveSHA256}, {previous, meta.PreviousSHA256}} {
		got, err := binaryDigest(check.path)
		if err != nil {
			return err
		}
		if got != check.want {
			return fmt.Errorf("rollback metadata does not match %s; use the manual upgrade runbook", check.path)
		}
	}
	if st, err := os.Stat(meta.DataDir); err != nil || !st.IsDir() {
		return fmt.Errorf("cannot check the GoRun lock in %s; use the manual rollback runbook", meta.DataDir)
	}
	dataLock, err := kernel.LockDataDir(meta.DataDir)
	if errors.Is(err, kernel.ErrDataDirLocked) {
		return upgradeLockedError()
	}
	if err != nil {
		return err
	}
	defer dataLock.Release()
	var restored bool
	if len(meta.Themes) != 0 {
		if err := validateThemeRollback(meta.ThemesDir, meta.Themes); err != nil {
			return err
		}
		if meta.ThemesDir != dir.Name() {
			themeLock, err := lockUpgradeDirectory(meta.ThemesDir)
			if err != nil {
				return err
			}
			defer themeLock.Close()
		}
		root, err := os.OpenRoot(meta.ThemesDir)
		if err != nil {
			return err
		}
		defer root.Close()
		scratch, err := os.MkdirTemp(meta.ThemesDir, ".gorun-rollback-*")
		if err != nil {
			return err
		}
		renames := themeRenames{root: root}
		defer func() {
			if !restored {
				if err := renames.restore(); err != nil {
					result = errors.Join(result, fmt.Errorf("theme recovery files at %s: %w", scratch, err))
					return
				}
			}
			result = errors.Join(result, root.RemoveAll(filepath.Base(scratch)))
		}()
		for _, entry := range meta.Themes {
			if err := renames.move(entry.Slug, filepath.Join(filepath.Base(scratch), entry.Slug)); err != nil {
				return err
			}
			if !entry.Added {
				if err := renames.move(filepath.Join(".previous", entry.Slug), entry.Slug); err != nil {
					return err
				}
			}
		}
	}

	if err := os.Rename(previous, u.path); err != nil {
		return err
	}
	restored = true
	if len(meta.Themes) != 0 {
		fmt.Fprintf(u.out, "Rolled back bundled themes in %s.\n", meta.ThemesDir)
	}
	fmt.Fprintf(u.out, "Restored the %s executable. SQLite migrations stay applied and may be irreversible. Check compatibility with the current data before starting GoRun.\n", meta.Version)
	manifest := filepath.Join(meta.DataDir, "backups", meta.BackupStamp+".manifest.json")
	dest := filepath.Join(filepath.Dir(meta.DataDir), "gorun-restored-"+meta.BackupStamp)
	fmt.Fprintln(u.out, "For data recovery, preserve the current data first. Restore the pre-upgrade backup into a separate directory:")
	fmt.Fprintf(u.out, "  %s backup-restore --input %s --dest %s\n", quoteShell(u.path), quoteShell(manifest), quoteShell(dest))
	if err := os.Remove(record); err != nil {
		return fmt.Errorf("binary restored but could not remove rollback metadata: %w", err)
	}
	return dir.Sync()
}

func binaryDigest(path string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	st, err := os.Lstat(path)
	if err != nil {
		return digest, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return digest, fmt.Errorf("%s is not a regular executable", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func quoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
