import json
import os
import re
import glob

def parse_tool_call_string(call_str):
    """
    Parses a Python-like tool call string, e.g.:
      "mv(source='final_report.pdf', destination='temp')"
      "mean(numbers=[3, 16, 60])"
      "ls(a=True)"
      "sort('final_report.pdf')"
    Returns (tool_name, expected_args)
    """
    match = re.match(r"^([\w\.]+)\((.*)\)$", call_str.strip())
    if not match:
        return None, {}
        
    tool_name = match.group(1)
    args_str = match.group(2).strip()
    
    args = {}
    if not args_str:
        return tool_name, args
        
    # Handle single positional argument without keywords, e.g. "sort('final_report.pdf')"
    if "=" not in args_str:
        val = args_str.strip("'\"")
        if val.lower() == "true":
            return tool_name, {"query": [True]}
        elif val.lower() == "false":
            return tool_name, {"query": [False]}
        else:
            try:
                return tool_name, {"query": [float(val) if '.' in val else int(val)]}
            except ValueError:
                return tool_name, {"query": [val]}
                
    try:
        # Evaluate safely under limited globals
        safe_globals = {
            "__builtins__": None,
            "True": True,
            "False": False,
            "None": None,
            "true": True,
            "false": False
        }
        parsed = eval(f"dict({args_str})", safe_globals)
        
        # Wrap each value inside a list of acceptable values
        wrapped = {}
        for k, v in parsed.items():
            if isinstance(v, list):
                wrapped[k] = [v]
            else:
                wrapped[k] = [v]
        return tool_name, wrapped
    except Exception as e:
        # Fallback simple regex parsing if eval fails
        pairs = re.findall(r"(\w+)\s*=\s*('[^']*'|\"[^\"]*\"|\[[^\]]*\]|[\w\.\-]+)", args_str)
        wrapped = {}
        for k, v in pairs:
            val = v.strip("'\"")
            if val.lower() == "true":
                wrapped[k] = [True]
            elif val.lower() == "false":
                wrapped[k] = [False]
            elif val.startswith("[") and val.endswith("]"):
                # Simple list parsing
                try:
                    wrapped[k] = [json.loads(val.replace("'", '"'))]
                except Exception:
                    wrapped[k] = [val]
            else:
                try:
                    wrapped[k] = [float(val) if '.' in val else int(val)]
                except ValueError:
                    wrapped[k] = [val]
        return tool_name, wrapped

def convert_local_bfcl():
    src_dir = "internal/benchmark/testdata/bfcl"
    dest_dir = "internal/benchmark/testdata"
    answers_dir = os.path.join(src_dir, "possible-answers")
    
    if not os.path.exists(src_dir):
        print(f"Error: source directory '{src_dir}' does not exist.")
        return
    if not os.path.exists(answers_dir):
        print(f"Error: possible-answers directory '{answers_dir}' does not exist.")
        return
        
    answer_files = glob.glob(os.path.join(answers_dir, "BFCL_v4_*.json"))
    if not answer_files:
        print(f"Warning: No possible answer files found in {answers_dir}.")
        return
        
    # Load all real multi-turn function schemas
    real_schemas = {}
    func_docs_dir = "internal/benchmark/testdata/gorilla-main/berkeley-function-call-leaderboard/bfcl_eval/data/multi_turn_func_doc"
    if os.path.exists(func_docs_dir):
        doc_files = glob.glob(os.path.join(func_docs_dir, "*.json"))
        for doc_file in doc_files:
            with open(doc_file, "r") as df:
                for line in df:
                    if not line.strip():
                        continue
                    try:
                        t = json.loads(line)
                        name = t.get("name")
                        if not name:
                            continue
                        params = t.get("parameters", {})
                        if params.get("type") == "dict":
                            params["type"] = "object"
                        props = params.get("properties", {})
                        for pk, pv in props.items():
                            if isinstance(pv, dict) and pv.get("type") == "dict":
                                pv["type"] = "object"
                        real_schemas[name] = {
                            "name": name,
                            "description": t.get("description", ""),
                            "parameters": params
                        }
                    except Exception as e:
                        print(f"Error parsing schema line from {doc_file}: {e}")
        print(f"Successfully loaded {len(real_schemas)} real multi-turn function schemas from {func_docs_dir}.")
    else:
        print(f"Warning: multi_turn_func_doc directory not found at {func_docs_dir}.")

    converted_cases = []
    
    for ans_filepath in sorted(answer_files):
        filename = os.path.basename(ans_filepath)
        raw_filepath = os.path.join(src_dir, filename)
        
        if not os.path.exists(raw_filepath):
            print(f"Warning: Raw file {filename} not found in {src_dir}. Skipping.")
            continue
            
        if "memory" in filename or "web_search" in filename:
            print(f"Skipping non-function-calling dataset: {filename}")
            continue
            
        print(f"Processing local dataset file: {filename}...")
        
        # Load all possible answers for this file into a lookup map
        answers_by_id = {}
        with open(ans_filepath, "r") as f:
            for line in f:
                if not line.strip():
                    continue
                try:
                    record = json.loads(line)
                    answers_by_id[record["id"]] = record.get("ground_truth", [])
                except Exception as e:
                    print(f"Error parsing possible-answers line: {e}")
                    
        # Load raw dataset cases
        with open(raw_filepath, "r") as f:
            lines = f.readlines()
            
        is_multi_turn = "multi_turn" in filename
        translated_count = 0
        
        for line in lines:
            if not line.strip():
                continue
            try:
                raw_case = json.loads(line)
            except Exception:
                continue
                
            case_id = raw_case.get("id")
            if not case_id or case_id not in answers_by_id:
                continue
                
            ground_truth = answers_by_id[case_id]
            
            # Map tools/functions schema
            tools_list = []
            raw_funcs = raw_case.get("function", [])
            for t in raw_funcs:
                params = t.get("parameters", {})
                if params.get("type") == "dict":
                    params["type"] = "object"
                props = params.get("properties", {})
                for pk, pv in props.items():
                    if isinstance(pv, dict) and pv.get("type") == "dict":
                        pv["type"] = "object"
                tools_list.append({
                    "name": t.get("name", ""),
                    "description": t.get("description", ""),
                    "parameters": params
                })
                
            turns = []
            
            if is_multi_turn:
                # Reconstruct multi-turn steps
                question_turns = raw_case.get("question", [])
                questions = [q[0]["content"] for q in question_turns if q]
                
                for turn_idx, question in enumerate(questions):
                    if turn_idx >= len(ground_truth):
                        break
                    expected_calls_strings = ground_truth[turn_idx]
                    
                    turn_expected_calls = []
                    for call_str in expected_calls_strings:
                        expected_call, expected_args = parse_tool_call_string(call_str)
                        if expected_call:
                            # Register dynamic mock tool in tools_list if it doesn't already exist
                            exists = any(tool["name"] == expected_call for tool in tools_list)
                            if not exists:
                                if expected_call in real_schemas:
                                    tools_list.append(real_schemas[expected_call])
                                else:
                                    tools_list.append({
                                        "name": expected_call,
                                        "description": f"Dynamic BFCL benchmark tool mapping for {expected_call}",
                                        "parameters": {
                                            "type": "object",
                                            "properties": {
                                                "query": { "type": "string" }
                                            },
                                            "required": []
                                        }
                                    })
                            turn_expected_calls.append({
                                "tool_name": expected_call,
                                "args": expected_args
                            })
                    
                    legacy_call = ""
                    legacy_args = {}
                    if len(turn_expected_calls) == 1:
                        legacy_call = turn_expected_calls[0]["tool_name"]
                        legacy_args = turn_expected_calls[0]["args"]
                        
                    turns.append({
                        "user_message": question,
                        "expected_calls": turn_expected_calls,
                        "expected_tool_call": legacy_call,
                        "expected_args": legacy_args,
                        "mock_response": "{\"status\":\"success\"}"
                    })
            else:
                # Single-turn datasets
                question_turns = raw_case.get("question", [])
                user_msg = ""
                if len(question_turns) > 0 and len(question_turns[0]) > 0:
                    user_msg = question_turns[0][0].get("content", "")
                    
                turn_expected_calls = []
                for gt_dict in ground_truth:
                    if not gt_dict:
                        continue
                    for expected_call, expected_args in gt_dict.items():
                        turn_expected_calls.append({
                            "tool_name": expected_call,
                            "args": expected_args
                        })
                        
                legacy_call = ""
                legacy_args = {}
                if len(turn_expected_calls) == 1:
                    legacy_call = turn_expected_calls[0]["tool_name"]
                    legacy_args = turn_expected_calls[0]["args"]
                    
                turns.append({
                    "user_message": user_msg,
                    "expected_calls": turn_expected_calls,
                    "expected_tool_call": legacy_call,
                    "expected_args": legacy_args,
                    "mock_response": "{\"status\":\"success\"}"
                })
                        
            if turns:
                converted_cases.append({
                    "id": case_id,
                    "dataset": "bfcl",
                    "system_prompt": raw_case.get("system_prompt", "You are a helpful function-calling assistant."),
                    "tools": tools_list,
                    "turns": turns,
                    "initial_config": raw_case.get("initial_config", {})
                })
                translated_count += 1
                
        print(f"Successfully compiled {translated_count} test cases.")
        
    # Save the consolidated array of test cases to both target paths
    for target_file in ["bfcl_samples.json", "bfcl_full_samples.json"]:
        dest_path = os.path.join(dest_dir, target_file)
        with open(dest_path, "w") as f:
            json.dump(converted_cases, f, indent=2)
        print(f"Housed {len(converted_cases)} fully translated test cases in: {dest_path}")
        
    # Also save the lightweight test samples to keep the test environment fast!
    test_cases = []
    found_types = set()
    for c in converted_cases:
        c_type = c["id"].split("_")[0]
        if c_type not in found_types:
            test_cases.append(c)
            found_types.add(c_type)
        if len(test_cases) >= 3:
            break
            
    test_dest_path = os.path.join(dest_dir, "bfcl_test_samples.json")
    with open(test_dest_path, "w") as f:
        json.dump(test_cases, f, indent=2)
    print(f"Housed lightweight test sample in: {test_dest_path}")
    print(f"Successfully compiled all BFCL V4 dataset components!")

if __name__ == "__main__":
    convert_local_bfcl()
