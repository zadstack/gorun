package commerce

import (
	"context"
	"net/http"
)

// setupPage feeds pages/commerce/setup.html — a read-only launch checklist for
// Stripe, billing servers and sellable provisioning products.
type setupPage struct {
	StripeReady, WebhookReady                                     bool
	StripeFromEnv, WebhookFromEnv                                 bool
	StripeFromDB, WebhookFromDB                                   bool
	ServerCount, SellableProducts                                 int
	ReadyToSell                                                   bool
	WebhookURL, TestWebhookURL, LiveWebhookURL, ShopURL, Currency string
}

func (h *adminHandlers) setupData(ctx context.Context) setupPage {
	s := h.svc
	stripeDB := s.d.Settings.GetString(ctx, SettingStripeSecret, "") != ""
	webhookDB := s.d.Settings.GetString(ctx, SettingWebhookSecret, "") != ""
	stripeEnv := s.d.Config.Stripe.SecretKey != ""
	webhookEnv := s.d.Config.Stripe.WebhookSecret != ""
	stripeReady := s.stripeSecretKey(ctx) != ""
	webhookReady := s.webhookSecret(ctx) != ""

	serverCount := 0
	if servers, err := s.Servers(ctx); err == nil {
		serverCount = len(servers)
	}
	sellable := 0
	if products, err := s.Products(ctx, false); err == nil {
		for _, p := range products {
			if !p.Active || p.Kind != ProductProvisioning || p.PlanCode == "" || p.ServerID == "" || p.PriceMinor <= 0 {
				continue
			}
			sellable++
		}
	}
	base := s.baseURL(ctx)
	webhookBase := base + "/webhooks/stripe"
	return setupPage{
		StripeReady: stripeReady, WebhookReady: webhookReady,
		StripeFromEnv: stripeEnv && !stripeDB, WebhookFromEnv: webhookEnv && !webhookDB,
		StripeFromDB: stripeDB, WebhookFromDB: webhookDB,
		ServerCount: serverCount, SellableProducts: sellable,
		ReadyToSell: stripeReady && webhookReady && serverCount > 0 && sellable > 0,
		WebhookURL:  webhookBase, TestWebhookURL: webhookBase + "/test", LiveWebhookURL: webhookBase + "/live",
		ShopURL:  base + "/shop",
		Currency: s.currency(ctx),
	}
}

func (h *adminHandlers) setup(w http.ResponseWriter, r *http.Request) {
	h.tpl.Render(w, r, "pages/commerce/setup.html", h.setupData(r.Context()))
}
