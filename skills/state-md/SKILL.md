---
name: state-md
description: "Read and execute against a git-committed STATE.md task graph. Use when a project has a STATE.md file defining a task dependency graph with status tracking. Enables parallel dispatch of unblocked tasks, deterministic wave completion, and session-persistent project state without relying on agent memory."
---

# state-md — Project State Manager

Trigger: any session where a `STATE.md` file exists in the repo root, or when the user mentions "wave", "task graph", "STATE.md", or "what's next on the project".

**Core principle:** The STATE.md is the project manager. Git history is the audit trail. Neither depends on agent memory. Read it cold at the start of every session.

---

## STATE.md Schema

```yaml
tasks:
  - id: task-slug
    description: What this task does
    status: pending | in-flight | done | blocked
    depends_on: []           # list of task ids that must be done first
    parallel: true | false   # can run alongside other parallel tasks
```

---

## Session Start Protocol

**Always do this first when STATE.md exists:**

```bash
cat STATE.md
```

Then:
1. Identify all tasks with `status: pending` where every `depends_on` entry is `done`
2. These are the **immediately dispatchable** tasks
3. Tasks with `parallel: true` can all run at the same time
4. Tasks with `parallel: false` must run one at a time

Report to the user: "Wave N has X tasks ready. N are parallel, M must be sequential. Here are the unblocked tasks: [list]"

---

## Dispatching Tasks

Each task should run in an **isolated git worktree** so parallel tasks don't conflict:

```bash
# Create an isolated worktree for this task
git worktree add ../repo-wt-{task-id} -b {branch-prefix}/{task-id}
cd ../repo-wt-{task-id}

# Run your agent of choice in that directory
# e.g. with Claude Code:
#   claude
# e.g. with Codex:
#   codex
# e.g. with Copilot CLI:
#   copilot suggest ...
```

When describing the task to the agent, always include:
> "Do NOT modify STATE.md — the human updates it after the work is merged."

For parallel tasks: open multiple terminals, one worktree per task, run the agent in each.

---

## After a Task Completes

1. Review the output / PR
2. Merge it
3. Update STATE.md yourself — never ask an agent to do this:

```python
# Quick Python one-liner to mark a task done:
python3 -c "
import re, sys
slug = 'task-slug'  # replace with actual id
content = open('STATE.md').read()
content = re.sub(
    r'(id: ' + slug + r'.*?status:) pending',
    r'\1 done', content, flags=re.DOTALL
)
open('STATE.md', 'w').write(content)
print('done')
"
```

4. Add a one-line note to the Completed section
5. Commit: `git add STATE.md && git commit -m "state: {task-slug} done"`
6. Check for newly unblocked tasks and start the next round

---

## Worktree Cleanup

After a task is merged:
```bash
git worktree remove ../repo-wt-{task-id} --force
git push origin --delete {branch}
```

---

## Rules

1. **Human-only STATE.md writes.** Agents write code and open PRs. The human updates STATE.md after merge. Never delegate this.
2. **Worktree isolation.** Each parallel task gets its own worktree. Never run two concurrent tasks in the same directory.
3. **Verify YAML validity before committing:**
   ```bash
   python3 -c "
   import yaml, re
   content = open('STATE.md').read()
   m = re.search(r'\`\`\`yaml\n(.*?)\`\`\`', content, re.DOTALL)
   yaml.safe_load(m.group(1))
   print('valid')
   "
   ```
4. **New tasks go inside the YAML fence.** When adding tasks, place them inside the ` ```yaml ` block, not after the closing ` ``` `.
5. **Wave boundaries are implicit.** A wave ends when all pending tasks are done and newly unblocked tasks are identified. Update the phase header in STATE.md when moving to a new wave.

---

## Anti-Patterns

| ❌ Don't | ✅ Do instead |
|---|---|
| Ask an agent to update STATE.md | You update STATE.md after merge |
| Run two tasks in the same clone | Create a worktree per task |
| Append new tasks after the closing YAML fence | Insert inside the ` ```yaml ` block |
| Mark a task done when the PR is open | Mark done only after merge is confirmed |
| Store project state in chat history | Keep it in STATE.md — it survives context resets |

---

## Reference Implementation

Firn (`github.com/frostyard/firn`) uses this pattern throughout. The `STATE.md` in that repo is the canonical example of a well-structured task graph.

---

## Note for Pie/Miles users

If you are running this skill inside Pie with the `dispatch_subagent` tool available, you can automate the dispatch step:

```
dispatch_subagent({
  agent: "coder",
  workdir: "/path/to/repo-wt-task-slug",
  task: "Implement [description]...\n\nDo NOT modify STATE.md."
})
```

The worktree isolation and orchestrator-only STATE.md update rules still apply.
