# JEV decision worker

`jev-score` implements the JSONL protocol consumed by `pkg/decision`.
It uses libllama and the Jev-Style-0.8B-Decision-v3 GGUF model.
It reads verdict logits directly. It does not generate text.

## Build and run

Install a recent llama.cpp build with Qwen3.5 support and `n_outputs_max` in `llama_context_params`.
The local checks used Homebrew llama.cpp 9770 on Apple M2 Pro.

```sh
brew install llama.cpp
bash scripts/build_jev_score.sh
bin/jev-score --model /absolute/path/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf
```

For a custom installation, set `LLAMA_DIR` to its installed prefix.
The build script also requires the ggml headers and libraries used by that installation.
The JSON dependency is vendored, so compilation requires no JSON package download.

The worker emits one `status: ready` object after loading the model and context.
Send one request per line:

```json
{"question_type":"choice","prompt":"Which word is a programming language?","options":["Go","banana"],"state":{}}
```

Responses contain `answer`, `confidence`, `scores`, and `latency_ms`.
`scores` contains calibrated probabilities, as expected by `pkg/decision`.
`raw_scores` contains the unscaled yes-minus-no logit margins.
`temperature`, `input_tokens`, and `head_tokens` provide additional diagnostics.

| Question type | Options | Answer |
| --- | --- | --- |
| `choice` | One or more unique, nonempty strings | Exact selected string |
| `noul` | Omitted, or two descriptions ordered false then true | `"false"` or `"true"` |
| `score` | Two to ten level descriptions, ordered from level zero | Selected level index as a string |

`score` selects a discrete level. It does not invent a continuous value when levels are absent.
An optional `category` selects the publisher's calibration group. Missing or unknown groups use the published global temperature.

## Limits and errors

The default context is 4,096 tokens: room for the Go state budget plus the question and options.
`--n-ctx` can increase this limit to 25,600 tokens.
The question, options, and verdict section have a separate 2,048-token limit.
Oversized inputs return an error; the worker does not truncate or split them.

Requests have a 1 MiB line limit and at most 256 options.
Malformed requests return an `error` object without an answer. The worker then accepts the next line.
Startup failures return a nonzero process exit code without a ready response.
Diagnostics go to stderr. Stdout contains JSONL only.

`--threads` defaults to 4. `--n-batch` defaults to 1,024.
`--ngl 0` selects CPU execution; the default allows GPU offload.
Each request clears both the attention cache and recurrent state.
The process keeps the model loaded until stdin closes.

## Verify

The native tests require real model weights. Ordinary Go tests skip them.

```sh
TZRO_TEST_JEV_BIN="$PWD/bin/jev-score" \
TZRO_TEST_JEV_MODEL=/absolute/path/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf \
go test ./pkg/decision -run TestNativeJev -count=1
```

The tests cover changing answers, typed results, calibrated probabilities, small decode batches, Unicode, invalid requests, and recovery after budget errors.
These tests check implementation behavior. They do not establish decision accuracy on developer tasks.

The implementation follows the publisher's [renderer and readout contract](https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF).
Calibration data is pinned to revision `edf37c26a1098f83cf4264b8adbe0dca2d2ebb0c`.
A local parity check against that reference produced identical token counts and raw margins on four fixtures.
The fixtures covered choice, true/false, score, and Unicode input on the installed Q4_K_M model.
See [dependency notices](third_party/README.md).
