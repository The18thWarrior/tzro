package config

import (
	"os"
	"testing"
)

func TestGetOpenRouterAPIKey(t *testing.T) {
	t.Run("returns config value", func(t *testing.T) {
		old := GlobalConfig.OpenRouterAPIKey
		GlobalConfig.OpenRouterAPIKey = "sk-test-key"
		defer func() { GlobalConfig.OpenRouterAPIKey = old }()

		key := GetOpenRouterAPIKey()
		if key != "sk-test-key" {
			t.Errorf("expected sk-test-key, got %s", key)
		}
	})

	t.Run("falls back to env var", func(t *testing.T) {
		old := GlobalConfig.OpenRouterAPIKey
		GlobalConfig.OpenRouterAPIKey = ""
		defer func() { GlobalConfig.OpenRouterAPIKey = old }()

		os.Setenv("OPENROUTER_API_KEY", "sk-env-key")
		defer os.Unsetenv("OPENROUTER_API_KEY")

		key := GetOpenRouterAPIKey()
		if key != "sk-env-key" {
			t.Errorf("expected sk-env-key, got %s", key)
		}
	})

	t.Run("resolves dollar-prefix env ref", func(t *testing.T) {
		old := GlobalConfig.OpenRouterAPIKey
		GlobalConfig.OpenRouterAPIKey = "$MY_OR_KEY"
		defer func() { GlobalConfig.OpenRouterAPIKey = old }()

		os.Setenv("MY_OR_KEY", "sk-dollar-key")
		defer os.Unsetenv("MY_OR_KEY")

		key := GetOpenRouterAPIKey()
		if key != "sk-dollar-key" {
			t.Errorf("expected sk-dollar-key, got %s", key)
		}
	})
}

func TestGetJudgeModel(t *testing.T) {
	t.Run("returns config value", func(t *testing.T) {
		old := GlobalConfig.JudgeModel
		GlobalConfig.JudgeModel = "openai/gpt-5.6-luna"
		defer func() { GlobalConfig.JudgeModel = old }()

		model := GetJudgeModel()
		if model != "openai/gpt-5.6-luna" {
			t.Errorf("expected openai/gpt-5.6-luna, got %s", model)
		}
	})

	t.Run("returns empty when not configured", func(t *testing.T) {
		old := GlobalConfig.JudgeModel
		GlobalConfig.JudgeModel = ""
		defer func() { GlobalConfig.JudgeModel = old }()

		model := GetJudgeModel()
		if model != "" {
			t.Errorf("expected empty, got %s", model)
		}
	})
}
