#!/bin/bash
TASK_ID=$1
CONDITION=${2:-"cooperative"}
ITERATIONS=3
OUTPUT_BASE="benchmark_results_$(date +%Y%m%d_%H%M%S)"

echo "Starting evaluation loop for task: $TASK_ID ($ITERATIONS runs)"

TOTAL_SCORE=0
SUCCESS_COUNT=0

i=1
while [ $i -le $ITERATIONS ]; do
    RUN_DIR="$OUTPUT_BASE/run_$i"
    echo "--- Run $i ---"
    ./tzro compare --task "$TASK_ID" --condition "$CONDITION" --output "$RUN_DIR"
    sleep 2 # Let SQLite file locks release
    
    REPORT_FILE=$(ls $RUN_DIR/comparison_results_*.json 2>/dev/null | head -n 1)
    if [ -f "$REPORT_FILE" ]; then
        SCORE=$(jq -r ".[0].qualityScore" "$REPORT_FILE")
    else
        echo "Run $i: Report file not found"
        SCORE="null"
    fi
    
    if [ "$SCORE" != "null" ] && [ "$SCORE" != "" ] && [ $(echo "$SCORE > 0" | bc) -eq 1 ]; then
        echo "Run $i Score: $SCORE"
        TOTAL_SCORE=$(echo "$TOTAL_SCORE + $SCORE" | bc)
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
        i=$((i + 1))
    else
        echo "Run $i: Failed or scored 0. Retrying..."
        sleep 5
    fi
done

if [ $SUCCESS_COUNT -gt 0 ]; then
    AVERAGE=$(echo "scale=2; $TOTAL_SCORE / $SUCCESS_COUNT" | bc)
    echo "========================================"
    echo "Average Score for $TASK_ID: $AVERAGE"
    echo "========================================"
    echo "$TASK_ID,$AVERAGE" >> benchmark_summary.csv
else
    echo "Evaluation failed: No successful runs"
    exit 1
fi
