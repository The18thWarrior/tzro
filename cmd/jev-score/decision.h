// Typed request rendering and calibrated readout for macjev-render-v1.
#pragma once
#include "calibration.h"
#include "third_party/nlohmann/json.hpp"
#include <cmath>
#include <cstdint>
#include <functional>
#include <set>
#include <stdexcept>
#include <string>
#include <vector>

namespace jev {
using json = nlohmann::ordered_json;
using Tokens = std::vector<int32_t>;
using Encode = std::function<Tokens(const std::string&)>;
constexpr size_t max_options = 256;
constexpr size_t head_limit = 2048;
constexpr int context_limit = 25600;
struct Rendered {
    Tokens tokens;
    std::vector<size_t> slots;
    std::vector<std::string> names;
    size_t head_tokens;
    double temperature;
};
inline bool blank(const std::string& s) {
    return s.find_first_not_of(" \t\r\n") == std::string::npos;
}
// Match json.dumps(ensure_ascii=False) separators, preserving punctuation in strings.
inline std::string state_json(const json& value) {
    if (!value.is_object() && !value.is_array()) return value.dump();
    std::string result = value.is_object() ? "{" : "[";
    for (auto it = value.begin(); it != value.end(); ++it) {
        if (it != value.begin()) result += ", ";
        if (value.is_object()) result += json(it.key()).dump() + ": ";
        result += state_json(it.value());
    }
    return result + (value.is_object() ? "}" : "]");
}
inline Rendered render(const json& request, const Encode& encode, size_t context_size) {
    if (!request.is_object()) throw std::runtime_error("request must be a JSON object");
    const auto type = request.at("question_type").get<std::string>();
    const auto prompt = request.at("prompt").get<std::string>();
    if (blank(prompt)) throw std::runtime_error("prompt must not be empty");
    std::vector<std::string> options;
    if (request.contains("options") && !request["options"].is_null())
        options = request["options"].get<std::vector<std::string>>();
    if (options.size() > max_options) throw std::runtime_error("at most 256 options are supported");
    Rendered result{};
    std::vector<std::string> descriptions;
    if (type == "choice") {
        if (options.empty()) throw std::runtime_error("choice requires options");
        std::set<std::string> seen;
        for (const auto& option : options) {
            if (blank(option) || !seen.insert(option).second)
                throw std::runtime_error("choice options must be nonempty and unique");
        }
        result.names = descriptions = options;
    } else if (type == "noul") {
        if (!options.empty() && options.size() != 2)
            throw std::runtime_error("noul options must be absent or two descriptions: false, true");
        result.names = {"false", "true"};
        const std::vector<std::string> defaults = {"no, the statement does not hold", "yes, the statement holds"};
        for (size_t i = 0; i < 2; ++i)
            descriptions.push_back(result.names[i] + ": " +
                (options.empty() || options[i].empty() ? defaults[i] : options[i]));
    } else if (type == "score") {
        if (options.size() < 2 || options.size() > 10)
            throw std::runtime_error("score requires 2..10 ordered level descriptions");
        for (size_t i = 0; i < options.size(); ++i) {
            result.names.push_back(std::to_string(i));
            descriptions.push_back("level " + std::to_string(i) + ": " + options[i]);
        }
    } else throw std::runtime_error("question_type must be choice, noul, or score");
    result.temperature = temperature(request.value("category", std::string{}), type, result.names.size());
    const auto append = [&](const Tokens& tokens) {
        result.tokens.insert(result.tokens.end(), tokens.begin(), tokens.end());
    };
    const auto state = request.value("state", json(nullptr));
    // Each segment is tokenized independently, without BOS/EOS or control tokens.
    append(encode("State:\n"));
    append(encode(state.is_string() ? state.get<std::string>() : state_json(state)));
    append(encode("\n\n"));
    const size_t prefix_size = result.tokens.size();
    append(encode("Question [" + type + "]: " + prompt + "\nOptions:\n"));
    std::vector<Tokens> encoded_options;
    for (const auto& description : descriptions) {
        encoded_options.push_back(encode(description));
        append(encode("- "));
        append(encoded_options.back());
        append(encode("\n"));
    }
    append(encode("Judge each option:\n"));
    for (const auto& option : encoded_options) {
        append(option);
        append(encode(" ->"));
        result.slots.push_back(result.tokens.size() - 1);
        append(encode("\n"));
    }
    result.head_tokens = result.tokens.size() - prefix_size;
    if (result.head_tokens > head_limit)
        throw std::runtime_error("question/options exceed the 2048-token head budget; nothing truncated");
    if (result.tokens.size() > context_size)
        throw std::runtime_error("input exceeds --n-ctx token budget; nothing truncated");
    return result;
}
inline json readout(const Rendered& input, const std::vector<double>& margins) {
    if (margins.size() != input.names.size() || margins.empty())
        throw std::runtime_error("missing verdict logits");
    for (double value : margins)
        if (!std::isfinite(value)) throw std::runtime_error("non-finite verdict logits");
    const auto best = std::max_element(margins.begin(), margins.end());
    const size_t winner = best - margins.begin();
    std::vector<double> probabilities;
    double sum = 0;
    for (double value : margins) {
        probabilities.push_back(std::exp((value - *best) / input.temperature));
        sum += probabilities.back();
    }
    json scores = json::object(), raw = json::object();
    for (size_t i = 0; i < margins.size(); ++i) {
        scores[input.names[i]] = probabilities[i] / sum;
        raw[input.names[i]] = margins[i];
    }
    // pkg/decision expects probabilities in scores; raw margins are separate.
    return {{"answer", input.names[winner]}, {"confidence", probabilities[winner] / sum},
            {"scores", scores}, {"raw_scores", raw}, {"temperature", input.temperature},
            {"input_tokens", input.tokens.size()}, {"head_tokens", input.head_tokens}};
}
} // namespace jev
