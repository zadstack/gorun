package commerce

import (
	"context"
	"errors"
	"strings"
)

const settingExampleProducts = "commerce.example_products_v1"

// exampleProductSlugs are inactive learning templates seeded once per site.
var exampleProductSlugs = []string{"example-provisioning", "example-service", "example-digital"}

func seedExampleProducts(ctx context.Context, s *service) error {
	if s.d.Settings.GetString(ctx, settingExampleProducts, "") != "" {
		return nil
	}
	cur := s.currency(ctx)
	for _, p := range exampleProducts(ctx, cur) {
		if _, err := s.Product(ctx, p.Slug); err == nil {
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.SaveProduct(ctx, p); err != nil {
			return err
		}
	}
	return s.d.Settings.Set(ctx, settingExampleProducts, "1")
}

func exampleProducts(ctx context.Context, currency string) []Product {
	_ = ctx
	return []Product{
		{
			Kind: ProductProvisioning, Slug: "example-provisioning",
			Name:             "Example: MCP access (provisioning)",
			ShortDescription: "Learn Stripe checkout + billing-server provisioning. Inactive until you map a server and plan.",
			Description: exampleDescription(
				"Provisioning products charge via Stripe, then create an entitlement on your mcp-billing/v1 server.",
				"Duplicate this product, pick your billing server and plan code, then activate it.",
				"منتجات التجهيز تخصم عبر Stripe ثم تنشئ استحقاقًا على خادم mcp-billing/v1.",
				"انسخ هذا المنتج، اختر خادم الفوترة ورمز الباقة، ثم فعّله.",
			),
			PriceMinor: 9900, Currency: currency, Active: false,
			GroupSlug: "learn", GroupName: "Examples", Sort: 10,
			PlanCode: "starter", AccessMonths: 1,
			Features: []string{"Stripe Checkout (one-time)", "Provisioning job → MCBB /accounts", "Customer magic link + Open"},
		},
		{
			Kind: ProductService, Slug: "example-service",
			Name:             "Example: Service request (no card charge)",
			ShortDescription: "Cart + manual review flow. Customers submit intake; operators fulfil later.",
			Description: exampleDescription(
				"Service products record a request without charging a card. Use intake fields to collect a brief.",
				"Activate when your intake schema is ready; fulfilment never implies payment.",
				"منتجات الخدمة تسجّل طلبًا دون خصم بطاقة. استخدم حقول intake لجمع الملخص.",
				"فعّل المنتج عندما يكون مخطط intake جاهزًا؛ التنفيذ لا يعني الدفع.",
			),
			PriceMinor: 25000, Currency: currency, Active: false,
			GroupSlug: "learn", GroupName: "Examples", Sort: 20,
			Intake:   []IntakeField{{Key: "brief", Label: "What do you need?", Required: true, MaxLength: 500}},
			Features: []string{"Cart checkout records pending request", "Email acknowledgement only", "Operator updates request status"},
		},
		{
			Kind: ProductDigital, Slug: "example-digital",
			Name:             "Example: Digital download",
			ShortDescription: "Paid file delivery after Stripe. Upload a private file before activating.",
			Description: exampleDescription(
				"Digital products require a private file ID and successful payment before /account/download works.",
				"Upload a file in the product editor, set download days, then activate.",
				"المنتجات الرقمية تحتاج ملفًا خاصًا ودفعًا ناجحًا قبل التنزيل من /account/download.",
				"ارفع ملفًا في محرر المنتج، حدّد أيام التنزيل، ثم فعّل المنتج.",
			),
			PriceMinor: 4900, Currency: currency, Active: false,
			GroupSlug: "learn", GroupName: "Examples", Sort: 30,
			DownloadDays: 7,
			Features:     []string{"Stripe payment required", "Signed download route", "Expiry after N days"},
		},
	}
}

func exampleDescription(enLead, enNext, arLead, arNext string) string {
	var b strings.Builder
	b.WriteString(`<p dir="ltr">`)
	b.WriteString(enLead)
	b.WriteString(`</p><p dir="ltr"><strong>Next:</strong> `)
	b.WriteString(enNext)
	b.WriteString(`</p><p dir="rtl">`)
	b.WriteString(arLead)
	b.WriteString(`</p><p dir="rtl"><strong>التالي:</strong> `)
	b.WriteString(arNext)
	b.WriteString(`</p>`)
	return b.String()
}

func isExampleProductSlug(slug string) bool {
	return IsExampleProductSlug(slug)
}

// IsExampleProductSlug reports inactive learning templates seeded on first boot.
func IsExampleProductSlug(slug string) bool {
	for _, s := range exampleProductSlugs {
		if slug == s {
			return true
		}
	}
	return false
}
