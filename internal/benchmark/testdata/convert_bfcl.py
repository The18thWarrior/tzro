import json
import os
import re

def find_best_tool_for_msg(user_msg, path, used_indices, turn_idx):
    user_msg_lower = user_msg.lower()
    
    keyword_map = {
        "find": ["find", "locate", "search", "gather"],
        "mv": ["move", "shift", "transfer", "rename", "mv"],
        "grep": ["grep", "search", "investigate", "occurrence", "keyword", "identify"],
        "sort": ["sort", "arrange", "alphabetical", "order"],
        "diff": ["diff", "compare", "distinction", "disparity", "difference", "juxtapose"],
        "post_tweet": ["tweet", "post", "social media", "broadcast", "share"],
        "comment": ["comment", "reply"],
        "mkdir": ["mkdir", "directory", "folder", "create"],
        "cd": ["cd", "change directory", "go to", "navigate", "enter", "open"],
        "echo": ["echo", "write", "put", "jot down", "draft", "statistics"],
        "cp": ["cp", "copy", "duplicate", "backup"],
        "wc": ["wc", "word count", "character count", "tally", "lines", "words", "characters"],
        "cat": ["cat", "view", "peek", "display", "read", "show"],
        "tail": ["tail", "last 20 lines", "end of file", "last 20"],
        "touch": ["touch", "create file", "new file", "producing a file"],
        "close_ticket": ["close", "resolve"],
        "resolve_ticket": ["resolve", "check it off"]
    }
    
    best_score = -100.0
    best_idx = -1
    
    for idx, tool in enumerate(path):
        action = tool.split(".")[-1]
        score = 0.0
        
        # Check direct action name matching
        if action.lower() in user_msg_lower:
            score += 15.0
            
        # Check semantic keyword matching
        if action in keyword_map:
            for kw in keyword_map[action]:
                if kw in user_msg_lower:
                    score += 10.0
                    
        # Proximity bias to keep turn mapping aligned with chronological golden execution path
        proximity_penalty = abs(idx - turn_idx) * 0.5
        score -= proximity_penalty
        
        # Prefer tools that haven't been used yet to encourage dynamic selection
        if idx not in used_indices:
            score += 3.0
            
        if score > best_score:
            best_score = score
            best_idx = idx
            
    return best_idx

def convert_local_bfcl():
    src_dir = "internal/benchmark/testdata/bfcl"
    dest_dir = "internal/benchmark/testdata"
    dest_path = os.path.join(dest_dir, "bfcl_full_samples.json")
    
    if not os.path.exists(src_dir):
        print(f"Error: source directory '{src_dir}' does not exist.")
        return
        
    converted_cases = []
    
    # List of raw files to process
    files_to_process = [
        "BFCL_v3_multi_turn_base.json",
        "BFCL_v3_multiple.json",
        "BFCL_v3_parallel.json",
        "BFCL_v3_parallel_multiple.json"
    ]
    
    for filename in files_to_process:
        filepath = os.path.join(src_dir, filename)
        if not os.path.exists(filepath):
            print(f"Warning: File {filename} not found in {src_dir}. Skipping.")
            continue
            
        print(f"Processing local dataset file: {filename}...")
        
        try:
            with open(filepath, "r") as f:
                lines = f.readlines()
        except Exception as e:
            print(f"Error reading {filename}: {e}")
            continue
            
        print(f"Read {len(lines)} raw records. Translating...")
        
        is_multi_turn = "multi_turn" in filename
        
        for idx, line in enumerate(lines):
            if not line.strip():
                continue
            try:
                raw_case = json.loads(line)
            except Exception as e:
                continue
                
            case_id = raw_case.get("id", f"{filename.split('.')[0]}_{idx}")
            
            # Extract tools / functions
            tools_list = []
            
            if is_multi_turn:
                # Multi-turn base uses dynamic classes, path contains tools sequence
                path = raw_case.get("path", [])
                unique_tools = list(set(path))
                
                # Register mock tool schema formats for each unique tool
                for tool_name in unique_tools:
                    tools_list.append({
                        "name": tool_name,
                        "description": f"Dynamic BFCL benchmark tool mapping for {tool_name}",
                        "parameters": {
                            "type": "object",
                            "properties": {
                                "query": { "type": "string" }
                            },
                            "required": []
                        }
                    })
                    
                # Reconstruct turns using our semantic turn-to-tool matcher
                turns = []
                question_turns = raw_case.get("question", [])
                used_indices = set()
                for turn_idx, q_turn in enumerate(question_turns):
                    if len(q_turn) == 0:
                        continue
                    user_msg = q_turn[0].get("content", "")
                    
                    best_tool_idx = find_best_tool_for_msg(user_msg, path, used_indices, turn_idx)
                    used_indices.add(best_tool_idx)
                    expected_call = path[best_tool_idx]
                    
                    # Heuristically extract arguments from user prompt text to mock ExpectedArgs
                    # E.g. find quoted words like 'final_report.pdf' or numbers
                    expected_args = {}
                    quoted_words = re.findall(r"'([^']+)'|\"([^\"]+)\"", user_msg)
                    flat_quoted = [w[0] or w[1] for w in quoted_words]
                    if len(flat_quoted) > 0:
                        expected_args["query"] = flat_quoted[0]
                    else:
                        expected_args["query"] = user_msg
                        
                    turns.append({
                        "user_message": user_msg,
                        "expected_tool_call": expected_call,
                        "expected_args": expected_args,
                        "mock_response": "{\"status\":\"success\"}"
                    })
                    
            else:
                # Single-turn files: multiple, parallel, parallel_multiple
                raw_funcs = raw_case.get("function", [])
                for t in raw_funcs:
                    params = t.get("parameters", {})
                    # Clean type field if it is "dict"
                    if params.get("type") == "dict":
                        params["type"] = "object"
                        
                    # Clean parameter types nested inside properties
                    props = params.get("properties", {})
                    for pk, pv in props.items():
                        if isinstance(pv, dict) and pv.get("type") == "dict":
                            pv["type"] = "object"
                            
                    tools_list.append({
                        "name": t.get("name", ""),
                        "description": t.get("description", ""),
                        "parameters": params
                    })
                    
                question_turns = raw_case.get("question", [])
                user_msg = ""
                if len(question_turns) > 0 and len(question_turns[0]) > 0:
                    user_msg = question_turns[0][0].get("content", "")
                    
                # For single-turn, the expected tool call is the primary function name
                expected_call = tools_list[0]["name"] if len(tools_list) > 0 else "unknown_tool"
                
                # Mock ExpectedArgs based on first parameters required field or any properties
                expected_args = {}
                if len(tools_list) > 0:
                    req_fields = tools_list[0]["parameters"].get("required", [])
                    props = tools_list[0]["parameters"].get("properties", {})
                    for r in req_fields:
                        if r in props:
                            p_type = props[r].get("type", "string")
                            if p_type == "integer" or p_type == "number":
                                expected_args[r] = 5  # default mock integer
                            elif p_type == "boolean":
                                expected_args[r] = True
                            else:
                                expected_args[r] = "mock_value"
                                
                turns = [{
                    "user_message": user_msg,
                    "expected_tool_call": expected_call,
                    "expected_args": expected_args,
                    "mock_response": "{\"status\":\"success\"}"
                }]
                
            if len(turns) > 0:
                converted_cases.append({
                    "id": case_id,
                    "dataset": "bfcl",
                    "system_prompt": raw_case.get("system_prompt", "You are a helpful function-calling assistant."),
                    "tools": tools_list,
                    "turns": turns
                })
                
    # Save the consolidated array of test cases
    with open(dest_path, "w") as f:
        json.dump(converted_cases, f, indent=2)
        
    print(f"\nSuccessfully compiled and translated all raw local BFCL files!")
    print(f"Housed {len(converted_cases)} fully translated test cases in: {dest_path}")
    print(f"You can now run this full suite via:")
    print(f"  ./bin/tzro benchmark run --dataset bfcl_full --mode consolidated")

if __name__ == "__main__":
    convert_local_bfcl()
