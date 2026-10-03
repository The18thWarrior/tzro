# Dependency notices

## nlohmann/json

`nlohmann/json.hpp` is the unmodified single header from nlohmann/json v3.11.3.
Source: https://github.com/nlohmann/json/tree/v3.11.3
License: [MIT](nlohmann/LICENSE.MIT).

The header is compiled into the worker. It adds no shared-library or Python dependency.

## JEV calibration

`../calibration.h` contains the temperature table from `readout_config.json` in
[chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF](https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF/tree/edf37c26a1098f83cf4264b8adbe0dca2d2ebb0c).
Upstream revision: `edf37c26a1098f83cf4264b8adbe0dca2d2ebb0c`.
The renderer implements that model's `macjev-render-v1` input format.
License: [Apache 2.0](JEV-LICENSE.txt).
The model weights and upstream reference scorer are not bundled here.
