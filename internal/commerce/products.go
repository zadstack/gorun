package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/media"
)

const (
	maxMonths        = 120
	maxNameLen       = 200
	maxFeatures      = 50
	productColumns   = `id, slug, name, description, short_description, price_minor, currency, active, group_slug, group_name, plan_code, COALESCE(server_id, ''), access_months, features_json, image_media_id, sort, seo_json, COALESCE(wp_product_id, ''), kind, digital_file, download_days, intake_json, billing_mode, stripe_price_id, billing_interval, trial_days, prices_json`
	productListOrder = ` ORDER BY sort, group_slug, name, id`
)

var priceRe = regexp.MustCompile(`^\d+(\.\d+)?$`)
var priceIDRe = regexp.MustCompile(`^price_[A-Za-z0-9_]+$`)

var slugRe = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{N}]+(?:-[\p{Ll}\p{Lo}\p{N}]+)*$`)

// Slugify lowercases s and joins its letter/digit runs with "-" (Arabic
// letters are kept).
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	return b.String()
}

func scanProduct(row interface{ Scan(...any) error }) (*Product, error) {
	var p Product
	var active int
	var features, seo, intake, prices string
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.ShortDescription, &p.PriceMinor, &p.Currency, &active,
		&p.GroupSlug, &p.GroupName, &p.PlanCode, &p.ServerID, &p.AccessMonths, &features, &p.ImageMediaID, &p.Sort, &seo, &p.WPProductID, &p.Kind, &p.DigitalFile, &p.DownloadDays, &intake,
		&p.BillingMode, &p.StripePriceID, &p.Interval, &p.TrialDays, &prices); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("commerce: scan product: %w", err)
	}
	p.Active = active == 1
	_ = json.Unmarshal([]byte(features), &p.Features)
	_ = json.Unmarshal([]byte(seo), &p.SEO)
	_ = json.Unmarshal([]byte(intake), &p.Intake)
	_ = json.Unmarshal([]byte(prices), &p.Prices)
	if p.Prices == nil {
		p.Prices = map[string]CurrencyPrice{}
	}
	if p.BillingMode == "" {
		p.BillingMode = "payment"
	}
	if p.Features == nil {
		p.Features = []string{}
	}
	if p.SEO == nil {
		p.SEO = map[string]any{}
	}
	return &p, nil
}

func (s *service) Products(ctx context.Context, activeOnly bool) ([]Product, error) {
	q := `SELECT ` + productColumns + ` FROM products`
	if activeOnly {
		q += ` WHERE active = 1`
	}
	rows, err := s.db.QueryContext(ctx, q+productListOrder)
	if err != nil {
		return nil, fmt.Errorf("commerce: list products: %w", err)
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Product returns the product with slug (active or not).
func (s *service) Product(ctx context.Context, slug string) (*Product, error) {
	return scanProduct(s.db.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE slug = ?`, slug))
}

func (s *service) productByID(ctx context.Context, id string) (*Product, error) {
	return scanProduct(s.db.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE id = ?`, id))
}

// Groups returns active products grouped by GroupSlug. Products are
// ordered by Sort (then group, name); groups appear in the order of their
// first product, so the group holding the lowest Sort comes first.
func (s *service) Groups(ctx context.Context) ([]ProductGroup, error) {
	products, err := s.Products(ctx, true)
	if err != nil {
		return nil, err
	}
	var groups []ProductGroup
	idx := map[string]int{}
	for _, p := range products {
		i, ok := idx[p.GroupSlug]
		if !ok {
			i = len(groups)
			idx[p.GroupSlug] = i
			groups = append(groups, ProductGroup{Slug: p.GroupSlug, Name: p.GroupName})
		}
		groups[i].Products = append(groups[i].Products, p)
	}
	return groups, nil
}

// normalize cleans p in place and returns field errors.
func (s *service) normalizeProduct(ctx context.Context, p *Product) map[string]string {
	fields := map[string]string{}
	if !validIntakeSchema(p.Intake) {
		fields["intake"] = "commerce.error.cart_invalid"
	}
	p.Kind = strings.TrimSpace(p.Kind)
	if p.Kind == "" {
		p.Kind = ProductProvisioning
	}
	if p.Kind != ProductProvisioning && p.Kind != ProductService && p.Kind != ProductDigital {
		fields["kind"] = "commerce.error.kind_invalid"
	}
	p.BillingMode = strings.TrimSpace(p.BillingMode)
	p.Interval = strings.TrimSpace(p.Interval)
	p.StripePriceID = strings.TrimSpace(p.StripePriceID)
	if p.Interval == "month" || p.Interval == "year" {
		p.BillingMode = "subscription"
	}
	if p.BillingMode == "" {
		p.BillingMode = "payment"
	}
	if p.BillingMode != "payment" && p.BillingMode != "subscription" {
		fields["billing_mode"] = "commerce.error.billing_mode"
	}
	if p.BillingMode == "subscription" && p.Interval != "month" && p.Interval != "year" {
		fields["interval"] = "commerce.error.interval"
	}
	if p.BillingMode != "subscription" {
		p.Interval = ""
		p.TrialDays = 0
	}
	if p.TrialDays < 0 || p.TrialDays > 730 {
		fields["trial_days"] = "commerce.error.trial_days"
	}
	if p.StripePriceID != "" && !priceIDRe.MatchString(p.StripePriceID) {
		fields["stripe_price_id"] = "commerce.error.price_id"
	}
	p.Prices = normalizePrices(p.Prices, fields)
	if p.DownloadDays == 0 {
		p.DownloadDays = 7
	}
	if p.DownloadDays < 1 || p.DownloadDays > 365 {
		fields["download_days"] = "commerce.error.days_invalid"
	}
	p.DigitalFile = strings.TrimSpace(p.DigitalFile)
	if p.DigitalFile != "" && !validDigitalKey(p.DigitalFile) {
		fields["digital_file"] = "commerce.error.file_invalid"
	}
	if p.Active && p.Kind == ProductDigital && !s.digitalFileExists(p.DigitalFile) {
		fields["digital_file"] = "commerce.error.file_required"
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Slug = strings.TrimSpace(p.Slug)
	// Description is stored as safe HTML (content's "cms" sanitizer policy),
	// so themes may render it with `safe`. ShortDescription is plain text.
	p.Description = content.Sanitize(p.Description)
	p.ShortDescription = strings.TrimSpace(p.ShortDescription)
	if p.Slug == "" {
		p.Slug = Slugify(p.Name)
	}
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	if p.Currency == "" {
		p.Currency = s.currency(ctx)
	}
	p.GroupName = strings.TrimSpace(p.GroupName)
	p.GroupSlug = strings.TrimSpace(p.GroupSlug)
	if p.GroupSlug == "" && p.GroupName != "" {
		p.GroupSlug = Slugify(p.GroupName)
	}
	p.PlanCode = strings.TrimSpace(p.PlanCode)
	p.ServerID = strings.TrimSpace(p.ServerID)
	if p.Kind != ProductProvisioning {
		p.PlanCode = ""
		p.ServerID = ""
	}
	if p.AccessMonths == 0 {
		p.AccessMonths = 1
	}
	var feats []string
	for _, f := range p.Features {
		if f = strings.TrimSpace(f); f != "" {
			feats = append(feats, f)
		}
	}
	p.Features = feats
	if p.Features == nil {
		p.Features = []string{}
	}
	if p.SEO == nil {
		p.SEO = map[string]any{}
	}
	switch {
	case p.Name == "":
		fields["name"] = "commerce.error.required"
	case utf8.RuneCountInString(p.Name) > maxNameLen:
		fields["name"] = "commerce.error.too_long"
	}
	if !slugRe.MatchString(p.Slug) {
		fields["slug"] = "commerce.error.slug_invalid"
	}
	if p.PriceMinor < 0 {
		fields["price"] = "commerce.error.price_invalid"
	}
	if !validCurrency(p.Currency) {
		fields["currency"] = "commerce.error.currency_invalid"
	}
	if p.AccessMonths < 1 || p.AccessMonths > maxMonths {
		fields["access_months"] = "commerce.error.months_invalid"
	}
	if len(p.Features) > maxFeatures {
		fields["features"] = "commerce.error.too_many"
	}
	if p.GroupSlug != "" && !slugRe.MatchString(p.GroupSlug) {
		fields["group_slug"] = "commerce.error.slug_invalid"
	}
	if p.Kind == ProductProvisioning && p.ServerID != "" {
		if _, err := s.server(ctx, p.ServerID); err != nil {
			fields["server_id"] = "commerce.error.server_unknown"
		}
	}
	if p.Active && p.Kind == ProductProvisioning && (p.PlanCode == "" || p.ServerID == "") {
		fields["active"] = "commerce.error.mapping_required"
	}
	return fields
}

func normalizePrices(in map[string]CurrencyPrice, fields map[string]string) map[string]CurrencyPrice {
	out := map[string]CurrencyPrice{}
	for code, pr := range in {
		cur := strings.ToUpper(strings.TrimSpace(code))
		if !validCurrency(cur) {
			fields["prices"] = "commerce.error.currency_invalid"
			continue
		}
		pr.PriceID = strings.TrimSpace(pr.PriceID)
		if pr.PriceID != "" && !priceIDRe.MatchString(pr.PriceID) {
			fields["prices"] = "commerce.error.price_id"
			continue
		}
		if pr.AmountMinor < 0 {
			fields["prices"] = "commerce.error.price_invalid"
			continue
		}
		if pr.AmountMinor == 0 && pr.PriceID == "" {
			continue
		}
		out[cur] = pr
	}
	return out
}

// priceFor picks the amount, currency and Stripe price for a checkout.
// A currency entry in Prices wins over the product default.
func (p Product) priceFor(currency string) (amount int64, cur, priceID string) {
	cur, amount, priceID = p.Currency, p.PriceMinor, p.StripePriceID
	if pr, ok := p.Prices[strings.ToUpper(currency)]; ok && (pr.AmountMinor > 0 || pr.PriceID != "") {
		if pr.AmountMinor > 0 {
			amount = pr.AmountMinor
			cur = strings.ToUpper(currency)
		}
		if pr.PriceID != "" {
			priceID = pr.PriceID
		}
	}
	return amount, cur, priceID
}

// SaveProduct creates (empty ID) or fully replaces a product.
func (s *service) SaveProduct(ctx context.Context, p Product) (*Product, error) {
	if fields := s.normalizeProduct(ctx, &p); len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	now := s.nowString()
	create := p.ID == ""
	if create {
		p.ID = db.NewID()
	}
	args := []any{p.Slug, p.Name, p.Description, p.ShortDescription, p.PriceMinor, p.Currency, boolInt(p.Active), p.GroupSlug, p.GroupName,
		p.PlanCode, nullable(p.ServerID), p.AccessMonths, jsonText(p.Features, "[]"), p.ImageMediaID, p.Sort, jsonText(p.SEO, "{}"), nullable(p.WPProductID), p.Kind, p.DigitalFile, p.DownloadDays, jsonText(p.Intake, "[]"),
		p.BillingMode, p.StripePriceID, p.Interval, p.TrialDays, jsonText(p.Prices, "{}")}
	var err error
	if create {
		_, err = s.db.ExecContext(ctx, `INSERT INTO products (slug, name, description, short_description, price_minor, currency, active, group_slug, group_name,
			plan_code, server_id, access_months, features_json, image_media_id, sort, seo_json, wp_product_id, kind, digital_file, download_days, intake_json,
			billing_mode, stripe_price_id, billing_interval, trial_days, prices_json, id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append(args, p.ID, now, now)...)
	} else {
		var res sql.Result
		res, err = s.db.ExecContext(ctx, `UPDATE products SET slug = ?, name = ?, description = ?, short_description = ?, price_minor = ?, currency = ?,
			active = ?, group_slug = ?, group_name = ?, plan_code = ?, server_id = ?, access_months = ?, features_json = ?, image_media_id = ?,
			sort = ?, seo_json = ?, wp_product_id = ?, kind = ?, digital_file = ?, download_days = ?, intake_json = ?,
			billing_mode = ?, stripe_price_id = ?, billing_interval = ?, trial_days = ?, prices_json = ?, updated_at = ? WHERE id = ?`, append(args, now, p.ID)...)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, ErrNotFound
			}
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: products.slug") {
			return nil, &ValidationError{Fields: map[string]string{"slug": "commerce.error.slug_taken"}}
		}
		return nil, fmt.Errorf("commerce: save product: %w", err)
	}
	s.audit.Record(ctx, "commerce.product.save", "product", p.ID, map[string]any{"slug": p.Slug, "created": create})
	return s.productByID(ctx, p.ID)
}

func (s *service) DeleteProduct(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("commerce: delete product: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.audit.Record(ctx, "commerce.product.delete", "product", id, nil)
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// WooCommerce meta keys. Both the spec names and the names used by
// mcp-billing-bridge 0.3.1 (class-mcbb-woocommerce.php) are accepted.
var (
	wpServerKeys = []string{"_mcbb_server_id", "_mcbb_server"}
	wpPlanKeys   = []string{"_mcbb_plan_code", "_mcbb_plan"}
	wpMonthKeys  = []string{"_mcbb_months"}
)

func firstMeta(meta map[string]string, keys []string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(meta[k]); v != "" {
			return v
		}
	}
	return ""
}

// UpsertFromWP creates or updates the product imported from WooCommerce
// product wp.ID (matched on wp_product_id, then on an unclaimed slug).
// Catalog fields are overwritten; Active, Sort, SEO and Features keep
// their GoRun values on update. The first category becomes the group;
// the first image is imported through media.FromURL when the media service
// is available. _mcbb_* meta map to plan code, months and server; a server
// reference only maps when it names an existing billing server id or name
// (WordPress server ids are not GoRun ids). New products are active only
// when plan and server are both mapped.
func (s *service) UpsertFromWP(ctx context.Context, wp WPProduct) (*Product, error) {
	if wp.ID <= 0 {
		return nil, &ValidationError{Fields: map[string]string{"id": "commerce.error.required"}}
	}
	wpID := strconv.Itoa(wp.ID)
	existing, err := scanProduct(s.db.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE wp_product_id = ?`, wpID))
	if slug := strings.ToLower(strings.TrimSpace(decodeSlug(wp.Slug))); errors.Is(err, ErrNotFound) && slug != "" {
		existing, err = scanProduct(s.db.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE slug = ? AND wp_product_id IS NULL`, slug))
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	p := Product{Active: false, Sort: 0, Features: []string{}, SEO: map[string]any{}}
	if existing != nil {
		p = *existing
	}
	p.WPProductID = wpID
	p.Slug = strings.ToLower(strings.TrimSpace(decodeSlug(wp.Slug)))
	if p.Slug == "" || !slugRe.MatchString(p.Slug) {
		p.Slug = Slugify(firstNonEmpty(p.Slug, wp.Name))
	}
	p.Name, p.Description, p.ShortDescription = wp.Name, wp.Description, wp.ShortDescription
	p.PriceMinor, p.Currency = wp.PriceMinor, strings.ToUpper(strings.TrimSpace(wp.Currency))
	if len(wp.Categories) > 0 {
		p.GroupName = strings.TrimSpace(wp.Categories[0])
		p.GroupSlug = Slugify(p.GroupName)
	}
	if v := firstMeta(wp.Meta, wpPlanKeys); v != "" {
		p.PlanCode = v
	}
	if v, err := strconv.Atoi(firstMeta(wp.Meta, wpMonthKeys)); err == nil {
		p.AccessMonths = min(max(v, 1), maxMonths)
	}
	if ref := firstMeta(wp.Meta, wpServerKeys); ref != "" {
		if id := s.matchServer(ctx, ref); id != "" {
			p.ServerID = id
		}
	}
	if existing == nil {
		p.Active = p.PlanCode != "" && p.ServerID != ""
	}
	if len(wp.ImageURLs) > 0 {
		p.ImageMediaID = s.importImage(ctx, wp.ImageURLs[0], wp.Name, p.ImageMediaID)
	}
	return s.SaveProduct(ctx, p)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// matchServer resolves a WordPress server reference to a billing server
// id: an exact id, else a case-insensitive name.
func (s *service) matchServer(ctx context.Context, ref string) string {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM billing_servers WHERE id = ? OR lower(name) = lower(?) ORDER BY id = ? DESC LIMIT 1`, ref, ref, ref).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// importImage imports url into the media library (deduped by source URL);
// on failure it keeps current.
func (s *service) importImage(ctx context.Context, url, alt, current string) string {
	svc, ok := s.media()
	if !ok || strings.TrimSpace(url) == "" {
		return current
	}
	m, err := svc.FromURL(ctx, url, media.UploadMeta{Alt: alt, SourceURL: url})
	if err != nil || m == nil {
		s.log.Warn("commerce: import product image", "url", url, "err", err)
		return current
	}
	return m.ID
}

// ParsePrice converts a major-unit amount ("12.5", "12,50") to minor units
// of currency. It rejects negatives and more decimals than the currency has.
func ParsePrice(s, currency string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return 0, nil
	}
	if !priceRe.MatchString(s) {
		return 0, fmt.Errorf("commerce: invalid price %q", s)
	}
	digits := minorDigits(currency)
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 {
		return 0, fmt.Errorf("commerce: invalid price %q", s)
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > digits {
		return 0, fmt.Errorf("commerce: price %q has too many decimals", s)
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	r.Mul(r, scale)
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("commerce: invalid price %q", s)
	}
	return r.Num().Int64(), nil
}

// FormatPrice renders minor units as a major-unit decimal string ("12.50").
func FormatPrice(minor int64, currency string) string {
	digits := minorDigits(currency)
	if digits == 0 {
		return strconv.FormatInt(minor, 10)
	}
	r := new(big.Rat).SetFrac(big.NewInt(minor), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	return r.FloatString(digits)
}

// ReplaceMediaRef re-points products.image_media_id from oldID to newID and
// audits commerce.product.media_replaced when anything changed.
func (s *service) ReplaceMediaRef(ctx context.Context, oldID, newID string) error {
	if oldID == "" || newID == "" || oldID == newID {
		return nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE products SET image_media_id = ?, updated_at = ? WHERE image_media_id = ?`, newID, s.nowString(), oldID)
	if err != nil {
		return fmt.Errorf("commerce: replace media ref: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.audit.Record(ctx, "commerce.product.media_replaced", "media", newID, map[string]any{"old_media": oldID, "products": n})
	}
	return nil
}
