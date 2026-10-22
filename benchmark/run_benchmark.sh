#!/bin/bash

LOG_FILE="benchmark_raw.log"
CSV_FILE="benchmark_results.csv"

echo "🚀 Starting benchmarks... Output is being displayed live and saved to $LOG_FILE"
echo "---------------------------------------------------------------------------"

# 1. Run Docker Compose. 
# 'tee' pipes the output to the terminal AND saves it to the log file simultaneously.
docker compose up | tee "$LOG_FILE"

echo "---------------------------------------------------------------------------"
echo "📊 Extracting metrics to $CSV_FILE..."

# 2. Initialize the CSV file with headers
echo "Language,Processed Records,Total Revenue,Execution Time (ms),Peak Memory Usage (MB)" > "$CSV_FILE"

# 3. Parse the log file line by line
# We use 'sed' to strip out hidden terminal color codes and Windows carriage returns (\r) first
sed -r 's/\x1B\[[0-9;]*[mK]//g; s/\r//g' "$LOG_FILE" | while IFS= read -r line; do
    
    if [[ "$line" =~ ===\ (.*)\ BENCHMARK\ RESULT\ === ]]; then
        LANG_NAME="${BASH_REMATCH[1]}"
    elif [[ "$line" =~ Processed\ Records[[:space:]]*:[[:space:]]*(.*) ]]; then
        RECORDS="${BASH_REMATCH[1]}"
    elif [[ "$line" =~ Total\ Revenue[[:space:]]*:[[:space:]]*(.*) ]]; then
        REVENUE="${BASH_REMATCH[1]}"
    elif [[ "$line" =~ Execution\ Time[[:space:]]*:[[:space:]]*(.*)\ ms ]]; then
        EXEC_TIME="${BASH_REMATCH[1]}"
    elif [[ "$line" =~ Peak\ Memory\ Usage[[:space:]]*:[[:space:]]*(.*)\ MB ]]; then
        MEMORY="${BASH_REMATCH[1]}"
        
        # When we hit the Peak Memory line, write the full row to the CSV
        echo "\"$LANG_NAME\",\"$RECORDS\",\"$REVENUE\",\"$EXEC_TIME\",\"$MEMORY\"" >> "$CSV_FILE"
    fi
done

echo "✅ Extraction complete! You can open $CSV_FILE in Excel or Google Sheets."