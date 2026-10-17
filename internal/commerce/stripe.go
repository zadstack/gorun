package commerce

import (
	"context"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v82"
)

// checkoutRequest is a one-time payment for one order.
type checkoutRequest struct {
	OrderID, OrderNumber, Email string
	ProductName                 string
	Currency                    string // ISO code, any case
	AmountMinor                 int64
	SuccessURL, CancelURL       string
}

// checkoutSession is the created Stripe Checkout Session.
type checkoutSession struct{ ID, URL string }

// checkoutCreator creates Stripe Checkout Sessions (faked in tests).
type checkoutCreator interface {
	Create(ctx context.Context, secretKey string, r checkoutRequest) (checkoutSession, error)
}

// stripeCheckout is the real Stripe implementation (mode=payment).
type stripeCheckout struct{}

func (stripeCheckout) Create(ctx context.Context, secretKey string, r checkoutRequest) (checkoutSession, error) {
	sc := stripe.NewClient(secretKey)
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		ClientReferenceID: stripe.String(r.OrderID),
		CustomerEmail:     stripe.String(r.Email),
		SuccessURL:        stripe.String(r.SuccessURL),
		CancelURL:         stripe.String(r.CancelURL),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:    stripe.String(strings.ToLower(r.Currency)),
				UnitAmount:  stripe.Int64(r.AmountMinor),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripe.String(r.ProductName)},
			},
		}},
		Metadata: map[string]string{"order_id": r.OrderID, "order_number": r.OrderNumber},
		PaymentIntentData: &stripe.CheckoutSessionCreatePaymentIntentDataParams{
			Metadata: map[string]string{"order_id": r.OrderID, "order_number": r.OrderNumber},
		},
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
