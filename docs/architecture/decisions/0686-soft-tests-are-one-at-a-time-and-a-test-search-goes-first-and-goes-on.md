# 0686. Soft tests run one at a time in the server, and a test search goes first and then goes on

November 27, 2026 · decided in P7-06

## The question

A provider that is out or failing is soft-tested, "never more than one test at a time per provider". Without a free check, "the next real search ... may go to it first, alone". How is one test at a time kept, what happens to the person's search when the test fails, and who runs the free checks?

## The decision

- **A test is claimed in the server's memory**, by the provider's place and name, for as long as it runs; a second search or sweep that finds it claimed leaves the provider as it is. One server serves an install, so memory is enough.
- **A test search goes to the provider first, alone**, and counts as one of `web.max_attempts`. If it fails, the provider stays out (or failing) with its wait doubled, and the search goes on to the ready providers, so the person still gets an answer. One search carries at most one test.
- **Free checks are run by the sweep alone**, every `web.test_tick`, started with the server (`web.StartSweep`, after Operations' sampler). A search never waits on a check. A check that finds quota left puts the provider back; one that finds none, or fails, doubles the wait; one whose key is refused makes it refused.
- The sweep's first pass also does the move of decision 0681.

## Why

- **The person's search is not the test's price.** Sending it to a provider that is probably still out, and stopping there, would turn every test into a failed search.
- **Checks cost nothing but time**, and a search should not spend its time on them.

## What it costs

- Two servers on one data folder could test one provider at once. Keystone runs one server for an install.
- A provider with a free check comes back only at the sweep's pace: up to `web.test_tick` after its test is due.
