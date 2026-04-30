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
4. Tasks with `parallel: false` must run one at a time (check if any in-flight)

Report to the user: "Wave N has X tasks ready. N are parallel, M must be sequential."

---

## Dispatching Tasks

For each unblocked task:
1. Create an isolated git worktree: `git worktree add ../repo-wt-{task-id} -b {branch-prefix}/{task-id}`
2. Dispatch subagent with `workdir` pointing to the worktree
3. **Always include in the task prompt:** "Do NOT modify STATE.md — the orchestrator updates it after merge."
4. Log dispatch: record subagent ID, task ID, and worktree path

Example dispatch pattern:
```
dispatch_subagent({
  agent: "coder",
  workdir: "/path/to/repo-wt-task-slug",
  task: "Implement [task description]...\n\nDo NOT modify STATE.md."
})
```

---

## After a Subagent Completes

1. Review the PR/output
2. Merge via your standard merge workflow
3. Update STATE.md yourself (never delegate this):
   ```python
   content = open('STATE.md').read()
   content = re.sub(
     r'(id: task-slug.*?status:) pending',
     r'\1 done',
     content, flags=re.DOTALL
   )
   open('STATE.md', 'w').write(content)
   ```
4. Add to the Completed section with a one-line summary
5. Commit: `git add STATE.md && git commit -m "state: {task-slug} done"`
6. Check for newly unblocked tasks and dispatch them

---

## Worktree Cleanup

After a task is merged and STATE.md updated:
```bash
git worktree remove /path/to/repo-wt-task-slug --force
git push origin --delete {branch}
```

---

## Rules

1. **Orchestrator-only STATE.md writes.** Subagents write code and open PRs. The orchestrator (you) updates STATE.md after merge. Never delegate this.
2. **Worktree isolation.** Each parallel task gets its own worktree. Never dispatch two concurrent tasks to the same working directory.
3. **Verify YAML validity before committing.** After editing STATE.md, validate the YAML block parses:
   ```bash
   python3 -c "import yaml, re; content=open('STATE.md').read(); m=re.search(r'\`\`\`yaml\n(.*?)\`\`\`',content,re.DOTALL); yaml.safe_load(m.group(1)); print('valid')"
   ```
4. **New tasks go inside the YAML fence.** When adding tasks, place them inside the ` ```yaml ` block, not after the closing fence.
5. **Wave boundaries are implicit.** There are no explicit wave markers — a wave ends when all pending tasks are done and all newly unblocked tasks are identified. Update the phase header when moving to a new wave.

---

## Anti-Patterns

| ❌ Don't | ✅ Do instead |
|---|---|
| Let a subagent update STATE.md | Orchestrator updates STATE.md after merge |
| Dispatch two tasks to the same clone | Create a worktree per task |
| Append new tasks after the closing YAML fence | Insert inside the ` ```yaml ` block |
| Assume a task is done because its PR is open | Mark done only after merge is confirmed |
| Keep STATE.md in a memory file | Keep it in git — it's the source of truth |

---

## Reference Implementation

Firn (`github.com/frostyard/firn`) uses this pattern throughout. The `STATE.md` in that repo is the canonical example.
