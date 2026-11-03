package config

// SimpleModeIgnoresGroups preserves legacy simple-mode routing unless isolation
// is explicitly enabled. Billing and API-key authorization still use RunMode.
func SimpleModeIgnoresGroups(cfg *Config) bool {
	return cfg != nil && cfg.RunMode == RunModeSimple && !cfg.SimpleMode.EnforceGroupIsolation
}
