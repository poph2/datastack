// Package messages defines the commands and events exchanged between microservices over Go channels.
package messages

// ChannelBuffer absorbs write bursts during the fast-forward backfill without blocking the orchestrator.
const ChannelBuffer = 500

// CreateCustomer asks the Customer service to register a new account.
type CreateCustomer struct {
	FullName string
	Email    string
}

// RestockProduct asks the Inventory service to add stock, creating the product if the SKU is new.
type RestockProduct struct {
	SKU       string
	Name      string
	Quantity  int
	UnitPrice float64
}

// CreateOrder asks the Sales service to record a checkout.
type CreateOrder struct {
	CustomerID uint
	ProductID  uint
	Quantity   int
	UnitPrice  float64
}

// OrderCreated is emitted by the Sales service so Invoicing can bill the order.
type OrderCreated struct {
	OrderID    uint
	CustomerID uint
	Amount     float64
}

// ProcessPayment asks the Invoicing service to settle or fail a pending invoice.
type ProcessPayment struct {
	InvoiceID uint
	Succeeded bool
}
