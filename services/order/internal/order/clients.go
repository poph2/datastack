package order

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/poph2/datastack/pkgs/httpclient"
)

// Errors the downstream clients translate the peers' error codes into. Any
// other failure (transport error, timeout, 5xx, unexpected code) is returned
// as-is, and the checkout records it as UPSTREAM_ERROR.
var (
	ErrCustomerNotFound = errors.New("customer not found")
	ErrProductNotFound  = errors.New("product not found")
	ErrOutOfStock       = errors.New("out of stock")
	ErrPaymentDeclined  = errors.New("payment declined")
)

// Peer error codes this service branches on.
const (
	peerCodeCustomerNotFound = "CUSTOMER_NOT_FOUND"
	peerCodeProductNotFound  = "PRODUCT_NOT_FOUND"
	peerCodeOutOfStock       = "OUT_OF_STOCK"
	peerCodePaymentDeclined  = "PAYMENT_DECLINED"
	peerCodeAlreadyPaid      = "ALREADY_PAID"
)

// Customer is what the checkout needs from the Customer Service.
type Customer struct {
	Country     string `json:"country"`
	LoyaltyTier string `json:"loyalty_tier"`
	Status      string `json:"status"`
}

// customerActive is the only Customer Service status that may check out.
const customerActive = "ACTIVE"

// CustomerClient calls the Customer Service.
type CustomerClient struct{ client *httpclient.Client }

// NewCustomerClient wraps a client pointed at the Customer Service.
func NewCustomerClient(client *httpclient.Client) *CustomerClient {
	return &CustomerClient{client: client}
}

// Get returns the customer, or ErrCustomerNotFound.
func (c *CustomerClient) Get(ctx context.Context, userID uint64) (Customer, error) {
	var customer Customer
	err := c.client.Do(ctx, http.MethodGet, fmt.Sprintf("/customers/%d", userID), nil, &customer)
	if httpclient.ErrorCode(err) == peerCodeCustomerNotFound {
		return Customer{}, ErrCustomerNotFound
	}
	return customer, err
}

// InventoryClient calls the Inventory Service.
type InventoryClient struct{ client *httpclient.Client }

// NewInventoryClient wraps a client pointed at the Inventory Service.
func NewInventoryClient(client *httpclient.Client) *InventoryClient {
	return &InventoryClient{client: client}
}

type stockChange struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

// Reserve takes the stock and returns the unit price to charge, or
// ErrOutOfStock or ErrProductNotFound.
func (c *InventoryClient) Reserve(ctx context.Context, productID string, quantity int) (decimal.Decimal, error) {
	var reserved struct {
		UnitPrice decimal.Decimal `json:"unit_price"`
	}
	err := c.client.Do(ctx, http.MethodPost, "/inventory/reserve", stockChange{productID, quantity}, &reserved)
	switch httpclient.ErrorCode(err) {
	case peerCodeOutOfStock:
		return decimal.Zero, ErrOutOfStock
	case peerCodeProductNotFound:
		return decimal.Zero, ErrProductNotFound
	}
	return reserved.UnitPrice, err
}

// Release returns reserved stock after a failed payment.
func (c *InventoryClient) Release(ctx context.Context, productID string, quantity int) error {
	return c.client.Do(ctx, http.MethodPost, "/inventory/release", stockChange{productID, quantity}, nil)
}

// paymentStatusSuccess is the Payment Service status of a captured charge.
const paymentStatusSuccess = "SUCCESS"

// PaymentClient calls the Payment Service.
type PaymentClient struct{ client *httpclient.Client }

// NewPaymentClient wraps a client pointed at the Payment Service.
func NewPaymentClient(client *httpclient.Client) *PaymentClient {
	return &PaymentClient{client: client}
}

// Charge takes payment for the order, or returns ErrPaymentDeclined. The
// method is left to the Payment Service, which picks one by weight.
func (c *PaymentClient) Charge(ctx context.Context, orderID uint64, amount decimal.Decimal) error {
	body := struct {
		OrderID uint64 `json:"order_id"`
		Amount  string `json:"amount"`
	}{orderID, amount.StringFixed(moneyDecimals)}

	err := c.client.Do(ctx, http.MethodPost, "/payments/process", body, nil)
	switch httpclient.ErrorCode(err) {
	case peerCodePaymentDeclined:
		return ErrPaymentDeclined
	case peerCodeAlreadyPaid:
		// The order already has a successful charge, so it is paid. That can
		// only happen if a request was repeated, and paying twice is exactly
		// what the Payment Service's guard prevents.
		return nil
	}
	return err
}

// Paid reports whether the order has a successful payment. The reconciler
// asks before deciding the fate of an order stranded in PENDING.
func (c *PaymentClient) Paid(ctx context.Context, orderID uint64) (bool, error) {
	var list struct {
		Payments []struct {
			Status string `json:"status"`
		} `json:"payments"`
	}
	path := fmt.Sprintf("/payments?order_id=%d&status=%s", orderID, paymentStatusSuccess)
	if err := c.client.Do(ctx, http.MethodGet, path, nil, &list); err != nil {
		return false, err
	}
	return len(list.Payments) > 0, nil
}

// MarketingClient calls the Marketing Service.
type MarketingClient struct{ client *httpclient.Client }

// NewMarketingClient wraps a client pointed at the Marketing Service.
func NewMarketingClient(client *httpclient.Client) *MarketingClient {
	return &MarketingClient{client: client}
}

// RecordConversion links a paid order to the ad click that brought the user.
func (c *MarketingClient) RecordConversion(ctx context.Context, attributionID, orderID uint64, amount decimal.Decimal) error {
	body := struct {
		OrderID     uint64 `json:"order_id"`
		OrderAmount string `json:"order_amount"`
	}{orderID, amount.StringFixed(moneyDecimals)}
	return c.client.Do(ctx, http.MethodPut, fmt.Sprintf("/attributions/%d/order", attributionID), body, nil)
}

// ShipmentClient calls the Shipment Service.
type ShipmentClient struct{ client *httpclient.Client }

// NewShipmentClient wraps a client pointed at the Shipment Service.
func NewShipmentClient(client *httpclient.Client) *ShipmentClient {
	return &ShipmentClient{client: client}
}

// Create asks for the order to be shipped and returns the tracking number.
// The Shipment Service accepts immediately and packs in the background, and
// it is idempotent per order, so retrying is safe.
func (c *ShipmentClient) Create(ctx context.Context, orderID uint64, customer Customer) (string, error) {
	body := struct {
		OrderID            uint64 `json:"order_id"`
		DestinationCountry string `json:"destination_country"`
		LoyaltyTier        string `json:"loyalty_tier,omitempty"`
	}{orderID, customer.Country, customer.LoyaltyTier}

	var shipment struct {
		TrackingNumber string `json:"tracking_number"`
	}
	err := c.client.Do(ctx, http.MethodPost, "/shipments/create", body, &shipment)
	return shipment.TrackingNumber, err
}
