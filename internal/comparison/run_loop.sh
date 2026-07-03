#!/bin/bash
TASK_ID=$1
CONDITION="cooperative"
ITERATIONS=3
OUTPUT_BASE="benchmark_results_$(date +%Y%m%d_%H%M%S)"

echo "Starting evaluation loop for task: $TASK_ID ($ITERATIONS runs)"

TOTAL_SCORE=0
SUCCESS_COUNT=0

for i in $(seq 1 $ITERATIONS); do
    RUN_DIR="$OUTPUT_BASE/run_$i"
    echo "--- Run $i ---"
    ./tzro compare --task "$TASK_ID" --condition "$CONDITION" --output "$RUN_DIR"
    
    # Extract score from the report JSON
    # The report is at $RUN_DIR/comparison_results_*.json
    REPORT_FILE=$(ls $RUN_DIR/comparison_results_*.json | head -n 1)
    if [ -f "$REPORT_FILE" ]; then
        SCORE=$(jq -r ".[0].qualityScore" "$REPORT_FILE")
    else
        echo "Run $i: Report file not found"
        SCORE="null"
    fi
    
    if [ "$SCORE" != "null" ] && [ "$SCORE" != "" ]; then
        echo "Run $i Score: $SCORE"
        TOTAL_SCORE=$(echo "$TOTAL_SCORE + $SCORE" | bc)
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        echo "Run $i: Failed to extract score"
    fi
done

if [ $SUCCESS_COUNT -gt 0 ]; then
    AVERAGE=$(echo "scale=2; $TOTAL_SCORE / $SUCCESS_COUNT" | bc)
    echo "========================================"
    echo "Average Score for $TASK_ID: $AVERAGE"
    echo "========================================"
    
    # Save the result to a summary file
    echo "$TASK_ID,$AVERAGE" >> benchmark_summary.csv
else
    echo "Evaluation failed: No successful runs"
    exit 1
fi
