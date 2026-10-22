#include <iostream>
#include <vector>
#include <string>
#include <chrono>
#include <new>
#include <atomic>
#include <iomanip>
#include <cstdlib>
#include <string_view>
#include <charconv>
#include <cstring>
#include <locale>

// --- Global Memory Tracker ---
std::atomic<size_t> current_memory(0);
std::atomic<size_t> peak_memory(0);

void* operator new(size_t size) {
    current_memory += size;
    size_t current = current_memory.load();
    size_t peak = peak_memory.load();
    while (current > peak) {
        if (peak_memory.compare_exchange_weak(peak, current)) {
            break;
        }
    }
    return std::malloc(size);
}

void operator delete(void* memory, size_t size) noexcept {
    current_memory -= size;
    std::free(memory);
}
void operator delete(void* memory) noexcept {
    std::free(memory);
}

// Custom locale formatter to inject commas into numbers
struct CommaNumPunct : std::numpunct<char> {
    char do_thousands_sep() const override { return ','; }
    std::string do_grouping() const override { return "\3"; } // Group by 3 digits
};

struct Order {
    std::string order_id;
    double amount;
    const char* status;   
};

int main() {
    // Apply the comma formatter globally to std::cout
    std::cout.imbue(std::locale(std::cout.getloc(), new CommaNumPunct()));

    auto start = std::chrono::high_resolution_clock::now();

    std::vector<Order> records;
    records.reserve(1000000); 

    for (int i = 0; i < 1000000; ++i) {
        const char* status = (i % 2 == 0) ? "COMPLETED" : "PENDING";
        
        char buf[24];
        std::memcpy(buf, "ORD-", 4);
        auto res = std::to_chars(buf + 4, buf + 24, i);
        
        records.emplace_back(Order{
            std::string(buf, res.ptr - buf), 
            static_cast<double>(i % 500), 
            status
        });
    }

    double total_revenue = 0.0;
    int processed_count = 0;

    for (const auto& r : records) {
        if (std::string_view(r.status) == "COMPLETED") {
            total_revenue += r.amount;
            processed_count++;
        }
    }

    auto end = std::chrono::high_resolution_clock::now();
    std::chrono::duration<double, std::milli> elapsed = end - start;
    double peak_mb = peak_memory.load() / (1024.0 * 1024.0);

    std::cout << "=== C++ BENCHMARK RESULT ===\n";
    std::cout << "Processed Records : " << processed_count << "\n";
    std::cout << std::fixed << std::setprecision(2);
    std::cout << "Total Revenue     : $" << total_revenue << "\n";
    
    // Briefly reset format for ms/MB display to avoid "87.41 ms" becoming "87.41"
    std::cout.imbue(std::locale("C"));
    std::cout << "Execution Time    : " << elapsed.count() << " ms\n";
    std::cout << "Peak Memory Usage : " << peak_mb << " MB\n";

    return 0;
}