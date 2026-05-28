import json
import os
import sys

def evaluate(file_path):
    print(f"Evaluating: {file_path}")
    if not os.path.exists(file_path):
        print(f"File not found: {file_path}")
        return

    with open(file_path, 'r') as f:
        content = f.read()

    # Find the JSON list in the file.
    # The JSON list usually starts with '[' and ends with ']' on the last line.
    lines = content.strip().split('\n')
    json_str = None
    for line in reversed(lines):
        line_strip = line.strip()
        if line_strip.startswith('[') and line_strip.endswith(']'):
            json_str = line_strip
            break

    if not json_str:
        print("Could not find JSON array in the file!")
        return

    try:
        data = json.loads(json_str)
    except Exception as e:
        print(f"Failed to parse JSON: {e}")
        return

    total = len(data)
    passed_cases = [x for x in data if x.get('passed') is True]
    failed_cases = [x for x in data if x.get('passed') is False]

    passed_count = len(passed_cases)
    failed_count = len(failed_cases)
    pass_rate = (passed_count / total) * 100 if total > 0 else 0

    print("\n=== OVERALL SUMMARY ===")
    print(f"Total Test Cases: {total}")
    print(f"Passed: {passed_count} ({pass_rate:.1f}%)")
    print(f"Failed: {failed_count} ({100 - pass_rate:.1f}%)")

    # Planning Match & Parameter Match Analysis
    planning_matches = sum(1 for x in data if x.get('planningMatch') is True)
    parameter_matches = sum(1 for x in data if x.get('parameterMatch') is True)
    fuzzy_matches = sum(1 for x in data if x.get('fuzzyMatchUsed') is True)

    print(f"Planning Match: {planning_matches} ({planning_matches/total*100:.1f}%)")
    print(f"Parameter Match: {parameter_matches} ({parameter_matches/total*100:.1f}%)")
    print(f"Fuzzy Match Used: {fuzzy_matches} ({fuzzy_matches/total*100:.1f}%)")

    # Token usage
    local_prompt = sum(x.get('localTokens', {}).get('promptTokens', 0) for x in data)
    local_comp = sum(x.get('localTokens', {}).get('completionTokens', 0) for x in data)
    local_total = sum(x.get('localTokens', {}).get('totalTokens', 0) for x in data)

    cloud_prompt = sum(x.get('cloudTokens', {}).get('promptTokens', 0) for x in data)
    cloud_comp = sum(x.get('cloudTokens', {}).get('completionTokens', 0) for x in data)
    cloud_total = sum(x.get('cloudTokens', {}).get('totalTokens', 0) for x in data)

    grand_total = local_total + cloud_total

    print("\n=== TOKEN CONSUMPTION ===")
    print(f"Local Tokens:  Prompt: {local_prompt:,} | Completion: {local_comp:,} | Total: {local_total:,} ({local_total/grand_total*100:.1f}% of all tokens)" if grand_total > 0 else "0%")
    print(f"Cloud Tokens:  Prompt: {cloud_prompt:,} | Completion: {cloud_comp:,} | Total: {cloud_total:,} ({cloud_total/grand_total*100:.1f}% of all tokens)" if grand_total > 0 else "0%")
    print(f"Grand Total:   {grand_total:,}")

    # Latency/Duration stats
    durations = [x.get('executionDurationMs', 0) for x in data]
    avg_duration = sum(durations) / total if total > 0 else 0
    max_duration = max(durations) if durations else 0
    min_duration = min(durations) if durations else 0

    print("\n=== EXECUTION LATENCY ===")
    print(f"Average Duration: {avg_duration / 1000:.2f}s")
    print(f"Max Duration:     {max_duration / 1000:.2f}s")
    print(f"Min Duration:     {min_duration / 1000:.2f}s")

    # Analyze failure modes
    print("\n=== DETAILED FAILURE MODES ===")
    # 1. Failed Planning Match
    failed_planning = [x for x in failed_cases if not x.get('planningMatch')]
    # 2. Passed Planning but Failed Parameter Match
    failed_params = [x for x in failed_cases if x.get('planningMatch') and not x.get('parameterMatch')]

    print(f"Failures due to Planning Match failure: {len(failed_planning)} ({len(failed_planning)/failed_count*100:.1f}% of failures)")
    if len(failed_cases) < 30:
        for i, c in enumerate(failed_planning):
            print(f"  {i+1}. Case: {c['testCaseId']}")
            print(f"     Executed tools: {c.get('executedToolCalls')}")

    print(f"\nFailures due to Parameter Match failure (Planning passed): {len(failed_params)} ({len(failed_params)/failed_count*100:.1f}% of failures)")
    if len(failed_cases) < 30:
        for i, c in enumerate(failed_params):
            print(f"  {i+1}. Case: {c['testCaseId']}")
            print(f"     Executed tools: {c.get('executedToolCalls')}")

if __name__ == '__main__':
    path = sys.argv[1] if len(sys.argv) > 1 else 'benchmark_results_5_26_2026_19_01.json'
    evaluate(path)
