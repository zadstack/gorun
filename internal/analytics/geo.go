package analytics

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// GeoFile is the optional MaxMind country database the operator drops into
// the data dir. Without it every country is unknownCountry.
const GeoFile = "GeoLite2-Country.mmdb"

// unknownCountry is the country of an IP with no GeoIP answer.
const unknownCountry = "--"

// geo maps a client IP to an ISO 3166-1 alpha-2 country code, or
// unknownCountry.
type geo interface{ Country(ip string) string }

// nilGeo answers unknownCountry for every IP (no GeoIP file).
type nilGeo struct{}

func (nilGeo) Country(string) string { return unknownCountry }

// mmdbGeo looks countries up in a MaxMind database.
type mmdbGeo struct{ r *maxminddb.Reader }

func (g mmdbGeo) Country(ip string) string {
	if g.r == nil {
		return unknownCountry
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return unknownCountry
	}
	var code string
	if err := g.r.Lookup(addr.Unmap()).DecodePath(&code, "country", "iso_code"); err != nil || len(code) != 2 {
		return unknownCountry
	}
	return strings.ToUpper(code)
}

// openGeo opens <dataDir>/GeoLite2-Country.mmdb once. A missing file gives
// nilGeo silently; an unreadable one is logged and also gives nilGeo. The
// returned function closes the database.
func openGeo(dataDir string, log *slog.Logger) (geo, func()) {
	path := filepath.Join(dataDir, GeoFile)
	r, err := maxminddb.Open(path)
	if err != nil {
		if log != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn("analytics: GeoIP database unreadable; countries are unknown", "path", path, "err", err)
		}
		return nilGeo{}, func() {}
	}
	return mmdbGeo{r: r}, func() { _ = r.Close() }
}
