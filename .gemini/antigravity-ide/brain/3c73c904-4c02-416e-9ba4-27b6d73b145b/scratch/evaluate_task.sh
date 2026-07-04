#!/bin/bash
TASK_ID=$1
CONDITION=${2:-cooperative}
OUTPUT_DIR="benchmark_results_$(date +%Y%m%d_%H%M%S)"
mkdir -p "$OUTPUT_DIR"

TOTAL_SCORE=0
NUM_RUNS=3

for i in $(seq 1 $NUM_RUNS); do
    echo "Run $i for task $TASK_ID..."
    ./tzro compare --task "$TASK_ID" --condition "$CONDITION" --output "$OUTPUT_DIR/run_$i" | tee "$OUTPUT_DIR/run_$i.log"
    # Extract quality score. Format: "  Quality: 4.25/5.0"
    SCORE=$(grep "Quality:" "$OUTPUT_DIR/run_$i.log" | head -n 1 | awk '{print $2}' | cut -d'/' -f1)
    if [ -z "$SCORE" ]; then
        echo "Error: Could not extract score from run $i"
        exit 1
    fi
    echo "Score: $SCORE"
    TOTAL_SCORE=$(echo "$TOTAL_SCORE + $SCORE" | bc)
done

AVG_SCORE=$(echo "scale=2; $TOTAL_SCORE / $NUM_RUNS" | bc)
echo "Average Score for $TASK_ID: $AVG_SCORE"

if (( $(echo "$AVG_SCORE >= 4.00" | bc -l) )); then
    echo "PASS"
    exit 0
else
    echo "FAIL"
    exit 1
fi
