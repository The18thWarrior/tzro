#!/usr/bin/env python3
import json
import os
import sys
import math

def calculate_percentile(sorted_data, percent):
    if not sorted_data:
        return 0.0
    k = (len(sorted_data) - 1) * percent
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return float(sorted_data[int(k)])
    d0 = sorted_data[int(f)] * (c - k)
    d1 = sorted_data[int(c)] * (k - f)
    return float(d0 + d1)

def analyze_file(file_path):
    if not os.path.exists(file_path):
        print(f"Error: File not found at '{file_path}'")
        sys.exit(1)

    with open(file_path, "r", encoding="utf-8") as f:
        content = f.read()

    # The file might contain server startup logs before the final JSON list.
    # We find the JSON list starting with '[' and ending with ']' on the last lines.
    lines = content.strip().split("\n")
    json_str = None
    for line in reversed(lines):
        line_strip = line.strip()
        if line_strip.startswith("[") and line_strip.endswith("]"):
            json_str = line_strip
            break

    if not json_str:
        # Try finding the first '[' and last ']' if the file is one large block.
        start_idx = content.find("[")
        end_idx = content.rfind("]")
        if start_idx != -1 and end_idx != -1 and start_idx < end_idx:
            json_str = content[start_idx:end_idx+1]

    if not json_str:
        print("Error: Could not locate a valid JSON array in the file.")
        sys.exit(1)

    try:
        data = json.loads(json_str)
    except Exception as e:
        print(f"Error: Failed to parse JSON content: {e}")
        sys.exit(1)

    if not isinstance(data, list):
        print("Error: JSON content is not a list of test cases.")
        sys.exit(1)

    total_cases = len(data)
    if total_cases == 0:
        print("Error: The benchmark JSON array is empty.")
        sys.exit(0)

    # 1. Broad Categorization
    passed_cases = [x for x in data if x.get("passed") is True]
    failed_cases = [x for x in data if x.get("passed") is False]
    
    passed_count = len(passed_cases)
    failed_count = len(failed_cases)
    overall_pass_rate = (passed_count / total_cases) * 100

    print(f"\n==================================================================")
    print(f" BENCHMARK RUN ANALYSIS: {os.path.basename(file_path)}")
    print(f"==================================================================")
    print(f"Total Scenarios Evaluated: {total_cases}")
    print(f"Passed Scenarios:         {passed_count} ({overall_pass_rate:.2f}%)")
    print(f"Failed Scenarios:         {failed_count} ({100.0 - overall_pass_rate:.2f}%)")

    # 2. Stratification by Dataset
    datasets = {}
    for case in data:
        ds = case.get("dataset", "unknown")
        if ds not in datasets:
            datasets[ds] = []
        datasets[ds].append(case)

    print(f"\n--- STRATIFIED SUCCESS RATES BY DATASET ---")
    print(f"{'Dataset':<20} | {'Total':<6} | {'Passed':<6} | {'Pass Rate':<10}")
    print("-" * 50)
    for ds_name, cases in sorted(datasets.items()):
        ds_total = len(cases)
        ds_passed = sum(1 for x in cases if x.get("passed") is True)
        ds_rate = (ds_passed / ds_total) * 100
        print(f"{ds_name:<20} | {ds_total:<6} | {ds_passed:<6} | {ds_rate:.2f}%")

    # 3. Planning & Parameter Match Analysis
    planning_matches = sum(1 for x in data if x.get("planningMatch") is True)
    parameter_matches = sum(1 for x in data if x.get("parameterMatch") is True)
    fuzzy_matches = sum(1 for x in data if x.get("fuzzyMatchUsed") is True)

    print(f"\n--- EXECUTION QUALITY METRICS ---")
    print(f"Planning Match:    {planning_matches:<4} / {total_cases:<4} ({planning_matches/total_cases*100:.2f}%)")
    print(f"Parameter Match:   {parameter_matches:<4} / {total_cases:<4} ({parameter_matches/total_cases*100:.2f}%)")
    print(f"Fuzzy Match Used:  {fuzzy_matches:<4} / {total_cases:<4} ({fuzzy_matches/total_cases*100:.2f}%)")

    # 4. Latency Performance metrics
    durations_ms = [float(x.get("executionDurationMs", 0)) for x in data if "executionDurationMs" in x]
    if durations_ms:
        sorted_durations = sorted(durations_ms)
        avg_latency_s = sum(durations_ms) / len(durations_ms) / 1000.0
        min_latency_s = sorted_durations[0] / 1000.0
        max_latency_s = sorted_durations[-1] / 1000.0
        p50_latency_s = calculate_percentile(sorted_durations, 0.50) / 1000.0
        p90_latency_s = calculate_percentile(sorted_durations, 0.90) / 1000.0
        p99_latency_s = calculate_percentile(sorted_durations, 0.99) / 1000.0

        print(f"\n--- EXECUTION LATENCY PROFILE ---")
        print(f"Average Latency:   {avg_latency_s:.3f}s")
        print(f"Median (p50):      {p50_latency_s:.3f}s")
        print(f"p90 Latency:       {p90_latency_s:.3f}s")
        print(f"p99 Latency:       {p99_latency_s:.3f}s")
        print(f"Min / Max Latency: {min_latency_s:.3f}s / {max_latency_s:.3f}s")
    else:
        print(f"\n--- EXECUTION LATENCY PROFILE ---\nNo duration metrics available.")

    # 5. Token Consumption Details
    local_prompt = sum(x.get("localTokens", {}).get("promptTokens", 0) for x in data if "localTokens" in x)
    local_comp = sum(x.get("localTokens", {}).get("completionTokens", 0) for x in data if "localTokens" in x)
    local_total = sum(x.get("localTokens", {}).get("totalTokens", 0) for x in data if "localTokens" in x)

    cloud_prompt = sum(x.get("cloudTokens", {}).get("promptTokens", 0) for x in data if "cloudTokens" in x)
    cloud_comp = sum(x.get("cloudTokens", {}).get("completionTokens", 0) for x in data if "cloudTokens" in x)
    cloud_total = sum(x.get("cloudTokens", {}).get("totalTokens", 0) for x in data if "cloudTokens" in x)

    grand_total = local_total + cloud_total

    print(f"\n--- TOKEN EFFICIENCY BREAKDOWN ---")
    if grand_total > 0:
        local_ratio = (local_total / grand_total) * 100
        cloud_ratio = (cloud_total / grand_total) * 100
        print(f"Local Sidecar Tokens:  {local_total:,} ({local_ratio:.2f}% of all tokens)")
        print(f"  └─ Prompt: {local_prompt:,} | Completion: {local_comp:,}")
        print(f"Cloud Engine Tokens:   {cloud_total:,} ({cloud_ratio:.2f}% of all tokens)")
        print(f"  └─ Prompt: {cloud_prompt:,} | Completion: {cloud_comp:,}")
        print(f"Grand Total Tokens:    {grand_total:,}")
    else:
        print("No token metrics available.")

    # 6. Failure Modes Analysis
    print(f"\n--- DETAILED FAILURE CLUSTERING ---")
    
    # Bucket A: Planning Match Failure
    failed_planning = [x for x in failed_cases if x.get("planningMatch") is False]
    # Bucket B: Passed Planning, but Parameter Match Failure
    failed_params = [x for x in failed_cases if x.get("planningMatch") is True and x.get("parameterMatch") is False]
    # Bucket C: Other Failures (Both planning and parameter passed, but overall marked failed)
    other_failures = [x for x in failed_cases if x.get("planningMatch") is True and x.get("parameterMatch") is True]

    print(f"1. Planning Failures (Wrong tool/sequence): {len(failed_planning)} ({len(failed_planning)/failed_count*100:.1f}% of failures)" if failed_count > 0 else "No planning failures.")
    print(f"2. Parameter Failures (Right tool, wrong args): {len(failed_params)} ({len(failed_params)/failed_count*100:.1f}% of failures)" if failed_count > 0 else "No parameter failures.")
    print(f"3. Operational Failures (Passed checks but failed execution): {len(other_failures)} ({len(other_failures)/failed_count*100:.1f}% of failures)" if failed_count > 0 else "No operational failures.")

    # Drill down into top failure cases if not too long
    if failed_count > 0:
        print(f"\n--- TOP FAILED SCENARIOS DETAILS (UP TO 10) ---")
        display_limit = min(10, failed_count)
        for i, case in enumerate(failed_cases[:display_limit]):
            fail_type = "Planning Mismatch" if case.get("planningMatch") is False else "Parameter Mismatch"
            if case.get("planningMatch") is True and case.get("parameterMatch") is True:
                fail_type = "Execution Output Mismatch"
            
            print(f"\n{i+1}. Test Case ID: {case.get('testCaseId')} [{case.get('dataset')}]")
            print(f"   Failure Category:  {fail_type}")
            print(f"   Execution Time:    {case.get('executionDurationMs', 0)/1000.0:.2f}s")
            print(f"   Executed Tools:    {', '.join(case.get('executedToolCalls', [])) or 'None'}")
            print(f"   Fuzzy Match Used:  {case.get('fuzzyMatchUsed')}")
            
    print(f"==================================================================\n")

if __name__ == "__main__":
    if len(sys.argv) < 2:
        # Fallback: look for benchmark results files in root
        default_file = None
        for filename in sorted(os.listdir("."), reverse=True):
            if filename.startswith("benchmark_results") and filename.endswith(".json"):
                default_file = filename
                break
        if default_file:
            print(f"No file supplied. Auto-detecting latest: '{default_file}'")
            analyze_file(default_file)
        else:
            print("Error: No benchmark result file supplied, and no local benchmark_results_*.json found.")
            sys.exit(1)
    else:
        analyze_file(sys.argv[1])
