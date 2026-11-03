package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLoadSimpleModeGroupIsolation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "isolated"}[enabled], func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("RUN_MODE", "simple")
			if enabled {
				t.Setenv("SIMPLE_MODE_ENFORCE_GROUP_ISOLATION", "true")
			}
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, enabled, cfg.SimpleMode.EnforceGroupIsolation)
			require.Equal(t, !enabled, SimpleModeIgnoresGroups(cfg))
			require.Equal(t, RunModeSimple, cfg.RunMode)
		})
	}
	require.False(t, SimpleModeIgnoresGroups(nil))
	require.False(t, SimpleModeIgnoresGroups(&Config{RunMode: RunModeStandard}))
}
