# Group isolation without enabling standard-mode billing

Legacy `RUN_MODE=simple` deliberately uses a platform-wide account pool. API keys
with different group IDs can therefore select the same downstream account.
Disabling an entry on another gateway does not disable that downstream account.

Set `SIMPLE_MODE_ENFORCE_GROUP_ISOLATION=true` on **every gateway instance** that
must honor API-key group membership while retaining simple-mode billing. The
YAML equivalent is `simple_mode.enforce_group_isolation: true`. The default is
false for compatibility. Standard mode is unchanged.

This option applies to scheduler snapshot buckets and rebuilds, database fallback,
sticky/previous-response selection, and model-availability checks. A request
without a group can only use ungrouped accounts; an empty group does not fall back
to the platform pool. Explicit configured fallback groups remain intentional
routing policy and must be reviewed separately.

Before enabling, check every active API key's group and its eligible account
memberships. Disable unfunded or unhealthy downstream accounts through the admin
API independently of any entry-level switch. Preserve current scheduling flags,
API keys, balances, and group assignments for rollback. Roll out the reviewed
build and setting together, preserving active streams through a drain window;
verify new traffic's selected account against the API key's group without
replaying historical messages. Never infer successful SSE completion from HTTP
200 alone.

Do not flip `RUN_MODE=standard` solely to repair routing: that also changes balance,
subscription, and billing enforcement. Turning off this isolation option restores
the legacy platform-wide pool, so keep unsafe accounts disabled during rollback.
