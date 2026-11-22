package cli

import "github.com/spf13/cobra"

const routingHelp = `How hiraishin decides which agent runs a job.

1. The job names a role
   A role (roles.<name> in preferences.toml) lists the providers it may use
   (allowed_providers, in order of preference), a minimum quality tier, and
   whether a job may move to another provider mid-task.

2. Filter: providers that cannot take the job are skipped
   - disabled, paused, draining, exhausted or in cooldown
   - not in the role's allowed_providers, or listed in deny_providers
   - quality_tier below the role's min_quality_tier
   - every slot busy (see Slots)
   - any quota window at or above reserve_used_pct (default 98%);
     a failover may go past it unless reserve_allow_failover = false
   - the job is pinned to another provider (hiraishin pin)

3. Score: the rest get a score, and the highest score wins
   Four factors, each from 0 to 1, multiplied by the active profile's weights:
     quality    quality_tier 1..5
     expiring   unused quota that resets soon, so it is spent before it is
                lost: remaining share x (1 - time to reset / window length)
     scarcity   how easy the quota is to replace: 60% from scarcity_tier,
                40% from the unused weekly share
     speed      speed_tier 1..5
   Then small adjustments:
     +0.03 max  position in allowed_providers (first gets the most)
     +0.15      hiraishin prefer <provider>
     -0.08      near limit: a window at or above warn_used_pct (default 85%)

4. Run and watch
   The agent runs in the job's git worktree. hiraishin watch reads its output.
   On a usage-limit message it stops the agent, keeps the worktree as it is,
   and routes the job again without that provider (failover). The next agent
   gets a prompt with the previous provider, the reason, git status, diff and
   the report. A role with never_switch_mid_task = true waits for the same
   provider's reset instead (WAITING_QUOTA). When no provider passes the
   filter, the job waits and is routed again at the earliest reset.

Slots
  max_concurrent is how many jobs one provider runs at the same time.
  "1/2 jobs" in status means 1 running and room for 1 more. A provider with
  every slot busy is skipped by the filter.

Pools
  Providers with the same shared_quota_pool draw from one subscription, for
  example opencode-glm-worker and opencode-glm-strong on zai-coding-plan.
  They share their usage windows, so status prints them once per pool.

Profiles (hiraishin profile set <name>)
  quality-first        quality 0.60  expiring 0.15  scarcity 0.15  speed 0.10
  burn-expiring-first  quality 0.20  expiring 0.50  scarcity 0.20  speed 0.10
  save-scarce          quality 0.20  expiring 0.25  scarcity 0.45  speed 0.10
  balanced             quality 0.35  expiring 0.30  scarcity 0.25  speed 0.10

See one decision in detail: hiraishin why <job_id>
`

func routingTopic() *cobra.Command {
	return &cobra.Command{Use: "routing", Short: "How jobs are routed: roles, filter, score, slots, pools, failover", Long: routingHelp}
}
