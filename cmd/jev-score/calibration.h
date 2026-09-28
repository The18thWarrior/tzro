// Jev-Style-0.8B-Decision-v3 calibration, Apache-2.0 upstream data.
// Source: chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF,
// revision edf37c26a1098f83cf4264b8adbe0dca2d2ebb0c, readout_config.json.
#pragma once
#include <algorithm>
#include <map>
#include <string>

namespace jev {
inline double temperature(const std::string& category, const std::string& type, size_t count) {
    static const std::pair<const char*, const char*> families[] = {
        {"typed_official", "typed"}, {"typed_synthetic", "typed_synth"},
        {"general_", "general"}, {"intent", "intent"}, {"nli", "nli"},
        {"theme_", "theme"}, {"mac_", "mac"}, {"long_", "long"},
    };
    static const std::map<std::string, double> groups = {
        {"general|choice|3-5", 0.85637969061011721},
        {"general|choice|6-10", 0.78833475918451323},
        {"general|noul|2", 0.97024745790386557},
        {"general|score|3-5", 0.9583320444900012},
        {"intent|choice|11-20", 0.85365869905152791},
        {"intent|choice|21+", 0.75087511818144659},
        {"long|choice|2", 1.0101601686032944},
        {"long|choice|3-5", 0.97894589258255793},
        {"long|choice|6-10", 0.69183326348500163},
        {"long|noul|2", 0.81832604602333137},
        {"long|score|3-5", 0.84128510940057888},
        {"mac|choice|3-5", 0.76404490613468656},
        {"mac|noul|2", 0.92246308025114299},
        {"mac|score|3-5", 2.7294507808118769},
        {"nli|choice|3-5", 1.0036116749615891},
        {"theme|choice|6-10", 0.98487291225229778},
        {"theme|noul|2", 0.89679949538603199},
        {"typed|choice|3-5", 0.9751541851571508},
        {"typed|noul|2", 0.98925890144503781},
        {"typed|score|3-5", 1.0056628114581752},
    };
    std::string family = "other";
    for (const auto& item : families) {
        if (category.rfind(item.first, 0) == 0) { family = item.second; break; }
    }
    const auto bucket = count <= 2 ? "2" : count <= 5 ? "3-5" :
                        count <= 10 ? "6-10" : count <= 20 ? "11-20" : "21+";
    const auto group = groups.find(family + "|" + type + "|" + bucket);
    return std::clamp(group == groups.end() ? 0.88005468217893323 : group->second, 0.3, 5.0);
}
} // namespace jev
