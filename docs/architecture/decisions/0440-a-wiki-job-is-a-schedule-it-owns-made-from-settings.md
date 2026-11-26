# 0440. A wiki job is a schedule it owns, made from the person's settings

November 25, 2026 · decided in P8-06

## The question

The plan runs the Waymarks agent-kit jobs as Keystone scheduled agents. The brief says each job's schedule, budget, model and on/off are settings, and that the jobs are off until switched on. P8-03 already runs schedules: due times in the person's zone, a missed run once, each run a background task in a chat of its own, and P8-05 gave each run a budget and the pause switch. How does a job get all of that without a second scheduler, and without its settings and its schedule saying two different things?

## The decision

**A job is an ordinary schedule that the job owns.** The table `wiki_jobs` names, for each job, the schedule made for it and a digest of the settings it was made from.

- The first time the person switches a job on (its setting `agents.wiki_<job>_on`, from the Scheduled screen, the route, or Settings → General), the runner makes the schedule through the scheduler's own functions: the job's title as its name, the job's instructions as its prompt, `agents.wiki_<job>_when` as its recurrence, its model (`agents.wiki_<job>_model`, or `routing.memory_model`), and `agents.wiki_<job>_budget_usd` as its own budget for each run.
- **The settings are the truth.** At each look (`agents.wiki_poll`), and at once from the routes, a change of a job's settings is carried to its schedule, once: the digest says whether they changed. A change the person makes to the schedule itself on the Scheduled screen stands until the settings change.
- **Off pauses it, and so does anything that keeps it from running**: the wrong store, no token of its own, no model, or a model that is not local or trusted. The digest takes in that reason too, so storing the token sets the schedule going again.
- A job never switched on has no schedule. Run now makes one, paused, so a run is always a schedule's run.

## Why

- Everything a run needs is already a schedule's: one late run for missed times, the person's zone, the run's chat and its record, the budget, the pause switch, a run resumed after a restart. A second scheduler would duplicate it and drift.
- The job's own state lives where every other setting does, and General draws a control for each with no screen work. The Scheduled screen's own controls still work on the schedule, as on any.

## What it costs

- A job's schedule appears in the Scheduled list as well as in the Wiki upkeep section. Editing it there is allowed but is overwritten when the job's settings next change.
- The prompt is copied into the schedule when it is made or changed; an install's change of `agents.wiki_<job>_prompt` reaches the schedule through the digest.
- Deleting the schedule while the job is on makes a new one at the next look.
