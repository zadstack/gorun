package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/update"
)

// releaseIndexDigest is the only digest form the update client accepts.
var releaseIndexDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// releaseIndex is the document internal/update.ReleaseIndex reads. Themes are
// left out on purpose: a site's active theme may be its own, and a list of
// the bundled themes would refuse updates for it. Absent means "unknown".
type releaseIndex struct {
	Latest   string                `json:"latest"`
	Releases []releaseIndexRelease `json:"releases"`
}

type releaseIndexRelease struct {
	Version    string                 `json:"version"`
	Artifacts  []releaseIndexArtifact `json:"artifacts"`
	Migrations []string               `json:"migrations"`
}

type releaseIndexArtifact struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type artifactFlags []releaseIndexArtifact

func (a *artifactFlags) String() string { return fmt.Sprint(len(*a)) }

func (a *artifactFlags) Set(v string) error {
	id, digest, ok := strings.Cut(v, "=")
	id, digest = strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(digest))
	if !ok || id == "" || !releaseIndexDigest.MatchString(digest) {
		return fmt.Errorf("want ID=sha256:<64 hex digits>, got %q", v)
	}
	for _, have := range *a {
		if have.ID == id {
			return fmt.Errorf("artifact %q listed twice", id)
		}
	}
	*a = append(*a, releaseIndexArtifact{ID: id, Digest: digest})
	return nil
}

// releaseIndexCmd prints the release index of this binary: its version, the
// artifacts the release pipeline built (with their digests) and every
// migration the binary ships, so a staged update can refuse a target that
// does not know the live data. It reads no database and no site.
func releaseIndexCmd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release-index", flag.ContinueOnError)
	var artifacts artifactFlags
	fs.Var(&artifacts, "artifact", "artifact as ID=sha256:DIGEST (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("release-index: unexpected arguments")
	}
	if !update.IsRelease(version) {
		return fmt.Errorf("release-index: %q is not a release version; build with -X main.version=X.Y.Z", version)
	}
	if len(artifacts) == 0 {
		return errors.New("release-index: give at least one --artifact ID=sha256:DIGEST")
	}
	migrations, err := kernel.KnownMigrationNames(Modules())
	if err != nil {
		return fmt.Errorf("release-index: %w", err)
	}
	v := strings.TrimPrefix(version, "v")
	doc := releaseIndex{Latest: v, Releases: []releaseIndexRelease{{Version: v, Artifacts: artifacts, Migrations: migrations}}}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
