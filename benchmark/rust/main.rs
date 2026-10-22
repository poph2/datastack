use std::alloc::{GlobalAlloc, Layout, System};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

struct PeakTracker {
    current: AtomicUsize,
    peak: AtomicUsize,
}

struct TrackingAllocator;

#[global_allocator]
static ALLOCATOR: TrackingAllocator = TrackingAllocator;

static STATS: PeakTracker = PeakTracker {
    current: AtomicUsize::new(0),
    peak: AtomicUsize::new(0),
};

unsafe impl GlobalAlloc for TrackingAllocator {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let ptr = System.alloc(layout);
        if !ptr.is_null() {
            let prev = STATS.current.fetch_add(layout.size(), Ordering::SeqCst);
            let current = prev + layout.size();
            
            let mut peak = STATS.peak.load(Ordering::SeqCst);
            while current > peak {
                match STATS.peak.compare_exchange_weak(peak, current, Ordering::SeqCst, Ordering::SeqCst) {
                    Ok(_) => break,
                    Err(p) => peak = p,
                }
            }
        }
        ptr
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        System.dealloc(ptr, layout);
        STATS.current.fetch_sub(layout.size(), Ordering::SeqCst);
    }
}

// Zero-dependency comma formatting helper
fn format_int(n: usize) -> String {
    let s = n.to_string();
    let mut result = String::with_capacity(s.len() + s.len() / 3);
    for (i, c) in s.chars().enumerate() {
        if i > 0 && (s.len() - i) % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }
    result
}

fn format_money(f: f64) -> String {
    let s = format!("{:.2}", f);
    let parts: Vec<&str> = s.split('.').collect();
    let int_part = parts[0];
    let mut result = String::with_capacity(int_part.len() + int_part.len() / 3);
    for (i, c) in int_part.chars().enumerate() {
        if i > 0 && (int_part.len() - i) % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }
    format!("{}.{}", result, parts[1])
}

#[allow(dead_code)]
struct Order {
    order_id: String,
    amount: f64,
    status: &'static str, 
}

fn main() {
    let start_time = Instant::now();

    let mut records: Vec<Order> = Vec::with_capacity(1_000_000);
    
    for i in 0..1_000_000 {
        let status = if i % 2 == 0 { "COMPLETED" } else { "PENDING" };

        let mut order_id = String::with_capacity(14);
        order_id.push_str("ORD-");
        order_id.push_str(&i.to_string());

        records.push(Order {
            order_id,
            amount: (i % 500) as f64,
            status,
        });
    }

    let mut total_revenue = 0.0;
    let mut processed_count = 0;

    for r in &records {
        if r.status == "COMPLETED" {
            total_revenue += r.amount;
            processed_count += 1;
        }
    }

    let elapsed_time = start_time.elapsed().as_secs_f64() * 1000.0;
    let peak_memory_mb = STATS.peak.load(Ordering::SeqCst) as f64 / (1024.0 * 1024.0);

    println!("=== RUST BENCHMARK RESULT ===");
    println!("Processed Records : {}", format_int(processed_count));
    println!("Total Revenue     : ${}", format_money(total_revenue));
    println!("Execution Time    : {:.2} ms", elapsed_time);
    println!("Peak Memory Usage : {:.2} MB", peak_memory_mb);
}