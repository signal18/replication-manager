# Global Jobs Dashboard

## What it solves
When you manage multiple clusters, checking job activity cluster by cluster is slow. The Global Jobs dashboard gives you one place to see:
- what is active right now,
- what just completed,
- whether any cluster has a current restic task.

## Where to find it
Open the web dashboard and go to:
- **Dashboard** (global view)
- **Global Jobs** accordion

## What you will see

### Active Jobs
Shows DB jobs that are still active or waiting for completion.

Columns:
- Cluster
- Server
- Task
- State
- Description
- Start
- End

Active jobs include these states:
- **Init**
- **Running**
- **Halted**

### Recently Done
Shows the most recent completed DB jobs across clusters.

Behavior:
- keeps a small recent history per cluster,
- then sorts the combined results so the newest completions appear first.

This helps you quickly answer questions like:
- what just finished?
- what just failed?

### Current Restic Tasks
Shows the current active restic task, when one exists.

Columns include:
- Cluster
- Task Type
- Status
- Phase
- Progress
- Started
- Error

## Access control
You only see data for clusters and sections your account is already allowed to access.

That means:
- if you cannot access a cluster's jobs, they do not appear here,
- if you cannot access a cluster's restic task state, it is omitted here as well.

## Limits and behavior
- The dashboard uses one aggregate request instead of polling each cluster separately from the browser.
- Restic history here is **current-task only**. This view is not a durable history of past restic work.
- The dashboard is read-only in this first version.

## Why this matters
This view reduces click-through, improves fleet-wide visibility, and helps operators see active and recently completed work quickly during normal operations and incident response.
