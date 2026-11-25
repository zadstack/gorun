package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

// exportCmd is gorun export --dest DIR [--no-pages] [--no-media]: the site
// as a folder to keep in Git (siteexport.go).
func exportCmd(args []string, out io.Writer) error {
	dest, args, err := stringFlag(args, "dest")
	if err != nil {
		return err
	}
	if dest == "" {
		return fmt.Errorf("export: --dest <directory> is required")
	}
	noPages, args := boolFlag(args, "no-pages")
	noMedia, args := boolFlag(args, "no-media")
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	rep, err := exportSite(ctx, app, version, ExportOptions{Dest: dest, Pages: !noPages, Media: !noMedia})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "exported %d items, %d layouts, %d media records (%d files) and %d pages to %s\n", rep.Items, rep.Layouts, rep.Media, rep.Files, rep.Pages, rep.Dest)
	for _, s := range rep.Skipped {
		fmt.Fprintf(out, "skipped: %s\n", s)
	}
	return nil
}
