package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Zero-dependency comma formatter for integers
func formatInt(n int) string {
	in := strconv.Itoa(n)
	var out []byte
	for i := 0; i < len(in); i++ {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, in[i])
	}
	return string(out)
}

// Zero-dependency comma formatter for currency
func formatMoney(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	parts := strings.Split(s, ".")
	in := parts[0]
	var out []byte
	for i := 0; i < len(in); i++ {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, in[i])
	}
	return string(out) + "." + parts[1]
}

type Order struct {
	OrderID string  `json:"order_id"`
	Amount  float64 `json:"amount"`
	Status  string  `json:"status"`
}

func main() {
	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	startTime := time.Now()

	records := make([]Order, 1_000_000)
	for i := 0; i < 1_000_000; i++ {
		status := "PENDING"
		if i%2 == 0 {
			status = "COMPLETED"
		}
		records[i] = Order{
			OrderID: fmt.Sprintf("ORD-%d", i),
			Amount:  float64(i % 500),
			Status:  status,
		}
	}

	var totalRevenue float64
	var processedCount int

	for i := 0; i < len(records); i++ {
		if records[i].Status == "COMPLETED" {
			totalRevenue += records[i].Amount
			processedCount++
		}
	}

	elapsedTime := time.Since(startTime).Seconds() * 1000

	runtime.ReadMemStats(&m2)
	peakMemoryMB := float64(m2.Alloc-m1.Alloc) / (1024 * 1024)

	fmt.Println("=== GO BENCHMARK RESULT ===")
	fmt.Printf("Processed Records : %s\n", formatInt(processedCount))
	fmt.Printf("Total Revenue     : $%s\n", formatMoney(totalRevenue))
	fmt.Printf("Execution Time    : %.2f ms\n", elapsedTime)
	fmt.Printf("Peak Memory Usage : %.2f MB\n", peakMemoryMB)
}