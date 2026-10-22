#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

size_t peak_memory = 0;

void* tracked_malloc(size_t size) {
    peak_memory = size; 
    return malloc(size);
}

typedef struct {
    char order_id[16];  
    double amount;
    const char* status; 
} Order;

void format_order_id(char* buf, int id) {
    memcpy(buf, "ORD-", 4);
    char* p = buf + 4;
    
    if (id == 0) {
        *p++ = '0';
        *p = '\0';
        return;
    }
    
    char temp[10];
    int len = 0;
    while (id > 0) {
        temp[len++] = '0' + (id % 10);
        id /= 10;
    }
    
    while (len > 0) {
        *p++ = temp[--len];
    }
    *p = '\0';
}

// Zero-dependency comma formatting
void format_int_with_commas(char* out, int num) {
    char temp[32];
    sprintf(temp, "%d", num);
    int len = strlen(temp);
    int out_idx = 0;
    for(int i = 0; i < len; i++) {
        if (i > 0 && (len - i) % 3 == 0) out[out_idx++] = ',';
        out[out_idx++] = temp[i];
    }
    out[out_idx] = '\0';
}

void format_money_with_commas(char* out, double num) {
    char temp[64];
    sprintf(temp, "%.2f", num);
    char* dot = strchr(temp, '.');
    int len = dot - temp;
    int out_idx = 0;
    for(int i = 0; i < len; i++) {
        if (i > 0 && (len - i) % 3 == 0) out[out_idx++] = ',';
        out[out_idx++] = temp[i];
    }
    strcpy(out + out_idx, dot);
}

int main() {
    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);

    int num_records = 1000000;
    Order* records = (Order*)tracked_malloc(sizeof(Order) * num_records);

    for (int i = 0; i < num_records; ++i) {
        format_order_id(records[i].order_id, i);
        records[i].amount = (double)(i % 500);
        records[i].status = (i % 2 == 0) ? "COMPLETED" : "PENDING";
    }

    double total_revenue = 0.0;
    int processed_count = 0;

    for (int i = 0; i < num_records; ++i) {
        if (records[i].status[0] == 'C') {
            total_revenue += records[i].amount;
            processed_count++;
        }
    }

    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed = (end.tv_sec - start.tv_sec) * 1000.0 + (end.tv_nsec - start.tv_nsec) / 1000000.0;
    double peak_mb = peak_memory / (1024.0 * 1024.0);

    // Format output buffers
    char count_str[32];
    char revenue_str[64];
    format_int_with_commas(count_str, processed_count);
    format_money_with_commas(revenue_str, total_revenue);

    printf("=== C BENCHMARK RESULT ===\n");
    printf("Processed Records : %s\n", count_str);
    printf("Total Revenue     : $%s\n", revenue_str);
    printf("Execution Time    : %.2f ms\n", elapsed);
    printf("Peak Memory Usage : %.2f MB\n", peak_mb);

    free(records);
    return 0;
}