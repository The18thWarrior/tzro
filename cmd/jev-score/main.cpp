// Native JSONL decision worker. Reads yes/no verdict logits without generating text.
#include "decision.h"
#include "llama.h"
#include <chrono>
#include <iostream>
#include <memory>

namespace {
constexpr int yes_token = 9542, no_token = 874, verdict_token = 1411;
struct Config {
    std::string model_path;
    int n_ctx = 4096; // State budget (2048) plus question/head budget.
    int n_threads = 4;
    int n_batch = 1024;
    int gpu_layers = 99;
};
int integer(const std::string& value, int min, int max) {
    size_t end = 0;
    const auto n = std::stol(value, &end);
    if (end != value.size() || n < min || n > max)
        throw std::runtime_error("numeric argument outside supported range: " + value);
    return static_cast<int>(n);
}
Config parse_args(int argc, char** argv) {
    Config config;
    for (int i = 1; i < argc; ++i) {
        const std::string flag = argv[i];
        if (i + 1 == argc) throw std::runtime_error("missing value for " + flag);
        const std::string value = argv[++i];
        if (flag == "--model" || flag == "-m") config.model_path = value;
        else if (flag == "--threads" || flag == "-t") config.n_threads = integer(value, 1, 1024);
        else if (flag == "--n-ctx") config.n_ctx = integer(value, 1, jev::context_limit);
        else if (flag == "--n-batch") config.n_batch = integer(value, 1, jev::context_limit);
        else if (flag == "--ngl") config.gpu_layers = integer(value, 0, 999);
        else throw std::runtime_error("unknown argument: " + flag);
    }
    if (config.model_path.empty()) throw std::runtime_error("--model is required");
    return config;
}
struct Backend {
    Backend() { llama_backend_init(); }
    ~Backend() { llama_backend_free(); }
};
class Scorer {
    std::unique_ptr<llama_model, decltype(&llama_model_free)> model_{nullptr, llama_model_free};
    std::unique_ptr<llama_context, decltype(&llama_free)> context_{nullptr, llama_free};
    const llama_vocab* vocab_ = nullptr;
    int n_batch_;
    int n_ctx_;
    jev::Tokens encode(const std::string& text) const {
        // parse_special=false prevents state text from introducing control tokens.
        int n = llama_tokenize(vocab_, text.data(), static_cast<int>(text.size()), nullptr, 0, false, false);
        if (n == 0) return {};
        if (n > 0 || n == INT32_MIN) throw std::runtime_error("tokenization failed");
        jev::Tokens tokens(-n);
        n = llama_tokenize(vocab_, text.data(), static_cast<int>(text.size()), tokens.data(),
                           static_cast<int>(tokens.size()), false, false);
        if (n < 0) throw std::runtime_error("tokenization failed");
        tokens.resize(n);
        return tokens;
    }
    std::vector<double> evaluate(const jev::Rendered& input) {
        // Clear attention KV and recurrent state between independent requests.
        llama_memory_clear(llama_get_memory(context_.get()), true);
        std::vector<double> margins(input.slots.size());
        for (size_t start = 0; start < input.tokens.size(); start += n_batch_) {
            const int count = static_cast<int>(std::min(input.tokens.size() - start, size_t(n_batch_)));
            llama_batch batch = llama_batch_init(count, 0, 1);
            batch.n_tokens = count;
            for (int j = 0; j < count; ++j) {
                batch.token[j] = input.tokens[start + j];
                batch.pos[j] = static_cast<llama_pos>(start + j);
                batch.n_seq_id[j] = 1;
                batch.seq_id[j][0] = 0;
                batch.logits[j] = 0;
            }
            for (size_t slot : input.slots)
                if (slot >= start && slot < start + count) batch.logits[slot - start] = 1;
            const int rc = llama_decode(context_.get(), batch);
            llama_batch_free(batch);
            if (rc != 0) throw std::runtime_error("llama_decode failed: " + std::to_string(rc));
            // Read each chunk before the next decode replaces its logits.
            for (size_t k = 0; k < input.slots.size(); ++k) {
                const size_t slot = input.slots[k];
                if (slot < start || slot >= start + count) continue;
                const float* logits = llama_get_logits_ith(context_.get(), static_cast<int>(slot - start));
                if (!logits) throw std::runtime_error("missing verdict logits");
                margins[k] = double(logits[yes_token]) - double(logits[no_token]);
            }
        }
        return margins;
    }
public:
    explicit Scorer(const Config& config) : n_batch_(std::min(config.n_batch, config.n_ctx)), n_ctx_(config.n_ctx) {
        auto params = llama_model_default_params();
        params.n_gpu_layers = config.gpu_layers;
        ggml_backend_dev_t cpu_only[] = {nullptr};
        if (config.gpu_layers == 0) params.devices = cpu_only;
        model_.reset(llama_model_load_from_file(config.model_path.c_str(), params));
        if (!model_) throw std::runtime_error("could not load GGUF model");
        vocab_ = llama_model_get_vocab(model_.get());
        for (const auto& item : std::vector<std::pair<std::string, int>>{{" yes", yes_token}, {" no", no_token}, {" ->", verdict_token}})
            if (encode(item.first) != jev::Tokens{item.second})
                throw std::runtime_error("model tokenizer does not match Jev-Style-v3 readout");
        auto ctx = llama_context_default_params();
        ctx.n_ctx = n_ctx_;
        ctx.n_batch = n_batch_;
        ctx.n_ubatch = std::min(n_batch_, 512);
        ctx.n_seq_max = 1;
        ctx.n_outputs_max = jev::max_options;
        ctx.n_threads = config.n_threads;
        ctx.n_threads_batch = config.n_threads;
        if (config.gpu_layers == 0) {
            ctx.offload_kqv = false;
            ctx.op_offload = false;
        }
        context_.reset(llama_init_from_model(model_.get(), ctx));
        if (!context_) throw std::runtime_error("could not create llama context");
    }
    jev::json handle(const jev::json& request) {
        const auto input = jev::render(request, [this](const auto& text) { return encode(text); }, n_ctx_);
        return jev::readout(input, evaluate(input));
    }
};
// Bound malformed requests without retaining an arbitrarily large line.
bool read_line(std::string& line, bool& oversized) {
    constexpr size_t limit = 1024 * 1024;
    line.clear();
    oversized = false;
    char c;
    bool any = false;
    while (std::cin.get(c)) {
        any = true;
        if (c == '\n') break;
        if (line.size() < limit) line.push_back(c);
        else oversized = true;
    }
    return any;
}
} // namespace
int main(int argc, char** argv) {
    std::ios_base::sync_with_stdio(false);
    std::cin.tie(nullptr);
    try {
        const auto config = parse_args(argc, argv);
        llama_log_set([](ggml_log_level level, const char* text, void*) {
            if (level >= GGML_LOG_LEVEL_WARN) std::cerr << text;
        }, nullptr);
        Backend backend;
        Scorer scorer(config);
        std::cout << jev::json({{"status", "ready"}, {"model", config.model_path},
                               {"template", "macjev-render-v1"}, {"n_ctx", config.n_ctx}}).dump() << std::endl;
        std::string line;
        bool oversized;
        while (read_line(line, oversized)) {
            if (line.empty() && !oversized) continue;
            const auto start = std::chrono::steady_clock::now();
            jev::json response;
            try {
                if (oversized) throw std::runtime_error("request exceeds 1 MiB JSONL limit");
                response = scorer.handle(jev::json::parse(line));
            } catch (const jev::json::exception&) {
                response = {{"error", "invalid JSON request or field type"}};
            } catch (const std::exception& error) {
                response = {{"error", error.what()}};
            }
            response["latency_ms"] = std::chrono::duration_cast<std::chrono::milliseconds>(
                std::chrono::steady_clock::now() - start).count();
            std::cout << response.dump() << std::endl;
        }
    } catch (const std::exception& error) {
        std::cerr << "jev-score: " << error.what() << '\n'
                  << "Usage: jev-score --model model.gguf [--n-ctx 4096] [--threads 4] [--n-batch 1024] [--ngl 99]\n";
        return 1;
    }
    return 0;
}
