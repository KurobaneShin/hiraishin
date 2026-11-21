# Naming Shortlist

Final name: `hiraishin` (chosen 2026-11-21; the working placeholder was `hiraishin`). The project targets an open-source portfolio release with a memorable anime/manga-inspired name whose metaphor maps directly onto quota scheduling and worktree failover.

> **Verification scope (2026-11-21):** Availability checks below use public web search over GitHub, npm, crates.io, and domain presence. No registrar/WHOIS queries were executed and no names, repositories, or domains were registered. Verify exact availability at the registrar and package registries immediately before claiming.

## Candidate Shortlist

| Candidate | Origin & Metaphor | Mapping to the tool | GitHub | npm | crates.io | Domains (`.dev`/`.sh`/`.io`) | Verdict |
|---|---|---|---|---|---|---|---|
| **`hiraishin`** | *Naruto* — Flying Thunder God (*Hiraishin no Jutsu*): mark a physical location with a seal and teleport instantaneously back to it. | **Exact fit.** The git worktree is the marked seal; on quota exhaustion, failover teleports the job to another agent at the exact marked worktree without losing work. | Small unrelated PyTorch-Lightning wrapper ([S-aiueo32/hiraishin](https://github.com/S-aiueo32/hiraishin)); **zero coding-agent or quota tools**. | **Appears free** (no exact top-level package match found). | **Appears free** (no exact crate match found). | No active dev-tool site found `[verify at registrar]` | **Top recommendation** |
| **`byakugan`** | *Naruto* — White Eye: 360-degree x-ray vision seeing chakra flow and tenketsu pathways. | Strong metaphor for **quota visibility / sensor inspection**, weaker for dispatch & failover. | Security ML model scanner ([Armur-Ai/byakugan](https://github.com/Armur-Ai/byakugan)), macOS OCR ([Custard/byakugan-scan](https://github.com/Custard/byakugan-scan)), Bazel watcher ([pulanski/byakugan](https://github.com/pulanski/byakugan)). | **Appears free** as exact top-level package. | **Appears free**. | No dominant tool site `[verify at registrar]` | **Best as sub-component name** (sensor/dashboard) |
| **`kagebunshin`** | *Naruto* — Shadow Clone: split one ninja's chakra pool across parallel clones; when a clone dispels, its experience returns. | Great metaphor for parallel agents sharing one quota pool. | **Collides directly in our domain:** [qkitzero/kage-bunshin](https://github.com/qkitzero/kage-bunshin) and [Gabrielrtads/kage-bunshin](https://github.com/Gabrielrtads/kage-bunshin) are already AI coding-agent worktree/swarm tools. | Appears free as exact package. | Appears free. | `[verify at registrar]` | **Avoid** (direct GitHub collision in AI worktree agents) |
| **`kuchiyose`** | *Naruto* — Summoning Technique: summon a specific creature suited for the fight. | Good metaphor for dispatching/summoning the right agent. | **Collides directly:** [lambdalisue/kuchiyose](https://github.com/lambdalisue/kuchiyose) is already an LLM summoning CLI; also [nikolat/kuchiyose](https://github.com/nikolat/kuchiyose). | Appears free. | **Taken** (`kuchiyose-normalize`, `kuchiyose-review`, `kuchiyose-metrics`). | `[verify at registrar]` | **Avoid** (LLM CLI collision + crates taken) |
| **`shikigami`** | Japanese folklore / *Jujutsu Kaisen* — paper spirits summoned to carry out tasks under rules. | Nice metaphor for policy-governed workers. | **Collides directly:** [ChiShengChen/shikigami](https://github.com/ChiShengChen/shikigami) (Claude Code multi-agent framework) and [Sannrox/shikigami](https://github.com/Sannrox/shikigami) (headless agent harness). | **Taken** (`@nahisaho/shikigami`, `@nrsk/shikigami`). | Appears free. | `[verify at registrar]` | **Avoid** (two direct coding-agent collisions) |
| **`toukakoukan`** | *Fullmetal Alchemist* — Equivalent Exchange: to obtain something, something of equal value must be given. | Maps to cost/quality/scarcity optimization. | Uncrowded. | Appears free. | Appears free. | `[verify at registrar]` | **Avoid** (11 letters, hard to spell/type as a CLI) |
| **`ekiden`** | Japanese long-distance relay race passing the *tasuki* sash between runners. | Good failover relay metaphor. | Academic/blockchain & race apps. | **Taken** (DeFi TypeScript SDK). | **Taken** (`ekiden`, `ekiden-grpcio`). | Taken in crypto/racing. | **Avoid** (npm + crates taken, and `tasuki` is already [0xkohe/tasuki](https://github.com/0xkohe/tasuki)) |
| **`senzu`** | *Dragon Ball* — Senzu Bean: instant full stamina/health restoration. | Maps to quota reset, not scheduling or failover. | [hakky54/senzu](https://github.com/hakky54/senzu) (packaged via Homebrew/PPA). | Appears free. | **Taken** (crypto wallet crate). | `[verify at registrar]` | **Avoid** (narrow metaphor + existing CLI) |
| **`kaioken`** | *Dragon Ball* — power multiplier with body-strain trade-off. | Maps to high concurrency / burst throughput. | [lance0/kaioken](https://github.com/lance0/kaioken) (popular HTTP load tester TUI), [Yofou/kaioken-core](https://github.com/Yofou/kaioken-core). | **Taken** (web/Inertia framework). | Appears free. | Taken. | **Avoid** (npm taken + crowded GitHub) |

## Recommendation

**Pick `hiraishin` as the project name** (with short binary alias `hr` or `hira` optional, or simply `hiraishin`), and optionally pair it with **same-universe sub-component names** where helpful:

- **Project / CLI:** **`hiraishin`** — zero collision in coding-agent orchestration on GitHub, npm, or crates.io; distinct, memorable, and its metaphor (marking a location and teleporting back to it) directly explains the core architectural differentiator ([ADR 0004](adr/0004-worktree-as-checkpoint.md): the git worktree is the mark, failover teleports the next agent onto it).
- **Optional internal/UI module names (same universe):**
  - **`byakugan`**: the sensor dashboard / v0.3 TUI (`hiraishin status` / TUI view showing every provider's rolling windows, burn rates, and time-to-reset).
  - **`fuiin`** (sealing formula) or plain `checkpoint`: the worktree checkpoint manifest written before failover (if desired; plain English names remain preferred in Go packages and CLI flags for contributor clarity).

### Pre-Registration Checklist (User Action)

Before making the repository public:
1. Confirm GitHub repo name (`<owner>/hiraishin`) and Go module path (`github.com/<owner>/hiraishin`).
2. Check domain availability at your registrar (`hiraishin.dev` or `hiraishin.sh`) if a docs site is planned.
3. Decide whether CLI invocation is `hiraishin` (clear, unambiguous) or provides a short shell alias (`hq` / `hr`).
