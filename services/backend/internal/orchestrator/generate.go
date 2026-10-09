package orchestrator

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/poph2/datastack/services/backend/internal/messages"
)

var (
	firstNames = []string{"Ada", "Grace", "Alan", "Linus", "Margaret", "Dennis", "Barbara", "Ken", "Frances", "Tim"}
	lastNames  = []string{"Lovelace", "Hopper", "Turing", "Torvalds", "Hamilton", "Ritchie", "Liskov", "Thompson", "Allen", "Berners-Lee"}

	productAdjectives = []string{"Classic", "Ultra", "Compact", "Pro", "Eco", "Smart"}
	productNouns      = []string{"Backpack", "Headphones", "Kettle", "Lamp", "Keyboard", "Bottle", "Jacket", "Speaker"}
)

// Daily volumes generated for each simulated day.
const (
	minNewCustomers, maxNewCustomers = 1, 5
	minRestocks, maxRestocks         = 1, 3
	minOrders, maxOrders             = 10, 30
	maxPaymentsPerDay                = 30
	maxOrderQuantity                 = 5
	paymentSuccessRate               = 0.9
	newProductChance                 = 0.3
)

func between(min, max int) int {
	return min + rand.IntN(max-min+1)
}

func newCustomer() messages.CreateCustomer {
	first := firstNames[rand.IntN(len(firstNames))]
	last := lastNames[rand.IntN(len(lastNames))]
	return messages.CreateCustomer{
		FullName: first + " " + last,
		Email:    fmt.Sprintf("%s.%s.%d@example.com", strings.ToLower(first), strings.ToLower(last), rand.Int64()),
	}
}

func newProduct(sequence int) messages.RestockProduct {
	name := productAdjectives[rand.IntN(len(productAdjectives))] + " " + productNouns[rand.IntN(len(productNouns))]
	return messages.RestockProduct{
		SKU:       fmt.Sprintf("SKU-%04d", sequence),
		Name:      name,
		Quantity:  between(50, 200),
		UnitPrice: float64(between(500, 20000)) / 100,
	}
}
