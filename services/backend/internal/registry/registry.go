// Package registry is the in-memory cache of entity IDs the orchestrator can safely reference.
package registry

import (
	"math/rand/v2"
	"sync"
)

// Product is the subset of product data the orchestrator needs to build orders and restocks.
type Product struct {
	ID        uint
	SKU       string
	UnitPrice float64
}

// Registry is populated by the microservices after successful writes and read by the orchestrator.
type Registry struct {
	mu              sync.RWMutex
	customers       []uint
	products        []Product
	pendingInvoices []uint
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{}
}

// AddCustomer records a customer ID that now exists.
func (r *Registry) AddCustomer(id uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.customers = append(r.customers, id)
}

// AddProduct records a product, replacing any earlier entry for the same ID.
func (r *Registry) AddProduct(p Product) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.products {
		if r.products[i].ID == p.ID {
			r.products[i] = p
			return
		}
	}
	r.products = append(r.products, p)
}

// AddPendingInvoice records an invoice that is waiting for payment.
func (r *Registry) AddPendingInvoice(id uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingInvoices = append(r.pendingInvoices, id)
}

// TakePendingInvoice removes and returns the oldest pending invoice.
func (r *Registry) TakePendingInvoice() (uint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pendingInvoices) == 0 {
		return 0, false
	}
	id := r.pendingInvoices[0]
	r.pendingInvoices = r.pendingInvoices[1:]
	return id, true
}

// RandomCustomer returns any known customer ID.
func (r *Registry) RandomCustomer() (uint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.customers) == 0 {
		return 0, false
	}
	return r.customers[rand.IntN(len(r.customers))], true
}

// RandomProduct returns any known product.
func (r *Registry) RandomProduct() (Product, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.products) == 0 {
		return Product{}, false
	}
	return r.products[rand.IntN(len(r.products))], true
}

// Counts returns how many customers, products, and pending invoices are known.
func (r *Registry) Counts() (customers, products, pendingInvoices int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.customers), len(r.products), len(r.pendingInvoices)
}
