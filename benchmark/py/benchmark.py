import time
import tracemalloc

def run_benchmark():
    # Start tracking memory and CPU time
    tracemalloc.start()
    start_time = time.perf_counter()

    # 1. Generate 1,000,000 records
    records = [
        {"order_id": f"ORD-{i}", "amount": float(i % 500), "status": "COMPLETED" if i % 2 == 0 else "PENDING"}
        for i in range(1_000_000)
    ]

    # 2. Process & Aggregate (Filter & Sum)
    total_revenue = 0.0
    processed_count = 0

    for r in records:
        if r["status"] == "COMPLETED":
            total_revenue += r["amount"]
            processed_count += 1

    # End measurements
    elapsed_time = (time.perf_counter() - start_time) * 1000  # ms
    _, peak_memory = tracemalloc.get_traced_memory()
    tracemalloc.stop()

    peak_memory_mb = peak_memory / (1024 * 1024)

    print("=== PYTHON BENCHMARK RESULT ===")
    print(f"Processed Records : {processed_count:,}")
    print(f"Total Revenue     : ${total_revenue:,.2f}")
    print(f"Execution Time    : {elapsed_time:.2f} ms")
    print(f"Peak Memory Usage : {peak_memory_mb:.2f} MB")

if __name__ == "__main__":
    run_benchmark()