package config

import "testing"

func TestGetDefaultTemperature(t *testing.T) {
	t.Run("returns hardcoded default when not configured", func(t *testing.T) {
		// Zero value means "use default"
		old := GlobalConfig.DefaultTemperature
		GlobalConfig.DefaultTemperature = 0.0
		defer func() { GlobalConfig.DefaultTemperature = old }()

		temp := GetDefaultTemperature()
		if temp != 1.0 {
			t.Errorf("expected 1.0 (hardcoded default), got %v", temp)
		}
	})

	t.Run("returns configured value when set", func(t *testing.T) {
		old := GlobalConfig.DefaultTemperature
		GlobalConfig.DefaultTemperature = 0.7
		defer func() { GlobalConfig.DefaultTemperature = old }()

		temp := GetDefaultTemperature()
		if temp != 0.7 {
			t.Errorf("expected 0.7, got %v", temp)
		}
	})

	t.Run("returns hardcoded default for negative value", func(t *testing.T) {
		old := GlobalConfig.DefaultTemperature
		GlobalConfig.DefaultTemperature = -1.0
		defer func() { GlobalConfig.DefaultTemperature = old }()

		temp := GetDefaultTemperature()
		if temp != 1.0 {
			t.Errorf("expected 1.0 (hardcoded default), got %v", temp)
		}
	})
}
