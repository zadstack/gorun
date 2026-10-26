package commerce

import (
	"context"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v82"
)

// checkoutRequest is one Checkout Session. Mode is "payment" (default) or
// "subscription". CustomerID reuses a stored Stripe customer when set.
type checkoutRequest struct {
	OrderID, OrderNumber, Email string
	ProductName                 string
	Currency                    string // ISO code, any case
	AmountMinor                 int64
	SuccessURL, CancelURL       string
	Mode                        string
	CustomerID                  string
	PriceID                     string
	Interval                    string
	TrialDays                   int
	AccountID, Plan, SiteID     string
	AutomaticTax                bool
}

// checkoutSession is the created Stripe Checkout Session.
type checkoutSession struct{ ID, URL string }

// checkoutCreator creates Stripe Checkout Sessions (faked in tests).
type checkoutCreator interface {
	Create(ctx context.Context, secretKey string, r checkoutRequest) (checkoutSession, error)
}

// stripeCheckout creates Checkout Sessions. One-time orders stay mode=payment.
type stripeCheckout struct{}

func (stripeCheckout) Create(ctx context.Context, secretKey string, r checkoutRequest) (checkoutSession, error) {
	sc := stripe.NewClient(secretKey)
	mode := string(stripe.CheckoutSessionModePayment)
	if r.Mode == "subscription" {
		mode = string(stripe.CheckoutSessionModeSubscription)
	}
	meta := map[string]string{"order_id": r.OrderID, "order_number": r.OrderNumber, "account_id": r.AccountID, "plan": r.Plan, "site_id": r.SiteID}
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(mode),
		ClientReferenceID: stripe.String(r.OrderID),
		SuccessURL:        stripe.String(r.SuccessURL),
		CancelURL:         stripe.String(r.CancelURL),
		LineItems:         []*stripe.CheckoutSessionCreateLineItemParams{lineItem(r)},
		Metadata:          meta,
	}
	if r.CustomerID != "" {
		params.Customer = stripe.String(r.CustomerID)
	} else {
		params.CustomerEmail = stripe.String(r.Email)
	}
	if mode == string(stripe.CheckoutSessionModePayment) {
		params.PaymentIntentData = &stripe.CheckoutSessionCreatePaymentIntentDataParams{Metadata: meta}
	} else {
		sub := &stripe.CheckoutSessionCreateSubscriptionDataParams{Metadata: meta}
		if r.TrialDays > 0 {
			sub.TrialPeriodDays = stripe.Int64(int64(r.TrialDays))
		}
		params.SubscriptionData = sub
	}
	if r.AutomaticTax {
		params.AutomaticTax = &stripe.CheckoutSessionCreateAutomaticTaxParams{Enabled: stripe.Bool(true)}
	}
	// Stripe Checkout picks the browser language ("auto"); Arabic is not a
	// supported Checkout locale.
	params.Locale = stripe.String("auto")
	cs, err := sc.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return checkoutSession{}, fmt.Errorf("commerce: stripe checkout session: %w", err)
	}
	return checkoutSession{ID: cs.ID, URL: cs.URL}, nil
}

func lineItem(r checkoutRequest) *stripe.CheckoutSessionCreateLineItemParams {
	item := &stripe.CheckoutSessionCreateLineItemParams{Quantity: stripe.Int64(1)}
	if r.PriceID != "" {
		item.Price = stripe.String(r.PriceID)
		return item
	}
	price := &stripe.CheckoutSessionCreateLineItemPriceDataParams{
		Currency:    stripe.String(strings.ToLower(r.Currency)),
		UnitAmount:  stripe.Int64(r.AmountMinor),
		ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripe.String(r.ProductName)},
	}
	if r.Mode == "subscription" {
		price.Recurring = &stripe.CheckoutSessionCreateLineItemPriceDataRecurringParams{Interval: stripe.String(r.Interval)}
	}
	item.PriceData = price
	return item
}

// portalCreator opens a Stripe Billing Portal session.
type portalCreator interface {
	Create(ctx context.Context, secretKey, customerID, returnURL string) (string, error)
}

type stripePortal struct{}

func (stripePortal) Create(ctx context.Context, secretKey, customerID, returnURL string) (string, error) {
	sc := stripe.NewClient(secretKey)
	ps, err := sc.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", fmt.Errorf("commerce: stripe billing portal: %w", err)
	}
	return ps.URL, nil
}

// refundCreator refunds a PaymentIntent in full. idempotencyKey must be
// stable per order (e.g. "refund:"+orderID) so concurrent admin clicks
// collapse to one Stripe Refund.
type refundCreator interface {
	Create(ctx context.Context, secretKey, paymentIntent, idempotencyKey string) (string, error)
}

type stripeRefunds struct{}

func (stripeRefunds) Create(ctx context.Context, secretKey, paymentIntent, idempotencyKey string) (string, error) {
	sc := stripe.NewClient(secretKey)
	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(paymentIntent),
		Reason:        stripe.String(string(stripe.RefundReasonRequestedByCustomer)),
	}
	if idempotencyKey != "" {
		params.SetIdempotencyKey(idempotencyKey)
	}
	rf, err := sc.V1Refunds.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("commerce: stripe refund: %w", err)
	}
	return rf.ID, nil
}
