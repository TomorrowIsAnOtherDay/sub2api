//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSimpleModeGroupIsolation(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "isolated"}[isolated], func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple, SimpleMode: config.SimpleModeConfig{EnforceGroupIsolation: isolated}}
			group := int64(2)
			repo := newBatchAccountQueryRepo()
			good := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, GroupIDs: []int64{2}}
			wrong := Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{9}}
			repo.results[batchAccountQueryKey{groupID: 2, platform: PlatformOpenAI}] = []batchAccountQueryResult{{accounts: []Account{good}}, {accounts: []Account{good}}}
			repo.results[batchAccountQueryKey{platform: PlatformOpenAI}] = []batchAccountQueryResult{{accounts: []Account{good, wrong}}, {accounts: []Account{good, wrong}}}
			svc := &SchedulerSnapshotService{cfg: cfg, accountRepo: repo}
			expected := int64(0)
			count := 2
			if isolated {
				expected = 2
				count = 1
			}
			require.Equal(t, expected, svc.normalizeGroupID(&group))
			require.Equal(t, []int64{expected}, svc.normalizeGroupIDs([]int64{2}))
			accounts, err := svc.loadAccountsFromDB(context.Background(), SchedulerBucket{GroupID: 2, Platform: PlatformOpenAI}, false)
			require.NoError(t, err)
			require.Len(t, accounts, count)
			gateway := &OpenAIGatewayService{cfg: cfg, accountRepo: repo}
			accounts, err = gateway.listSchedulableAccounts(context.Background(), &group, PlatformOpenAI)
			require.NoError(t, err)
			require.Len(t, accounts, count)
			require.True(t, gateway.openAIAccountMatchesSchedulingGroup(&good, &group))
			require.Equal(t, !isolated, gateway.openAIAccountMatchesSchedulingGroup(&wrong, &group), "sticky routing must not bypass isolation")
			require.Equal(t, config.RunModeSimple, cfg.RunMode, "isolation must not enable standard billing")
		})
	}
}
