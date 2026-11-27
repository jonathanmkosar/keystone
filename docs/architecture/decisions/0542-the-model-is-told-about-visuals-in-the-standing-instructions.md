# 0542. The model is told about charts and diagrams in the standing instructions, straight after the standing text

November 26, 2026 · decided in P9-04

## The question

A model writes a `chart` or a `mermaid` block only if it knows the page draws them and how a chart is written. Where is it told: in the standing instructions, in a tool's description, or in a tool of its own that returns the format when asked? And how is that kept from spoiling the part of a call a provider can keep (decision [0331](0331-the-seam-carries-cache-points-and-the-anthropic-adapter-makes-four-marks.md))?

## The decision

- **A setting holds the words**, `visuals.instructions`, and `turn.withVisuals` adds them to the standing text (`chats.context_system_text`) of every call, after a blank line, while the person's `visuals.draw` is on. Off, or with the words emptied, nothing is added.
- **They come straight after the standing text.** The line in `internal/turn/context.go` runs before the search-first rule is added (decision 0214), so the text block is: the standing text, then the visuals' words, then the search-first rule while a knowledge store is open. The context builder sends that block in the standing start, with the tools, the profile, the map, the project and the style, ahead of everything that changes from turn to turn.
- **The words say when, not only how**: "When a chart or a diagram would make an answer clearer than words alone, you may draw one in your reply; otherwise answer in words." Then the two formats, in under 150 words.
- **Each person may change them**, as they may the standing text (scope `tenant`).

## Why

- **The standing instructions are where every model reads how to answer**, whatever tools it has. A local model with no tool use still draws; a tool's description is read only by models that are offered the tool, and there is no tool here to hang it on. A tool that returns the format would cost a call before every chart and would make drawing a side trip.
- **The same words in every call** keep the standing start the same byte for byte, which is what a provider's cache keys on. Two calls of one chat are checked to have the same start, in `go test ./internal/turn` and in `verify`.
- **Before the search-first rule**, because that rule is there only while a knowledge store is open for the turn. Were the visuals' words after it, they would move when it came and went.
- **In a setting**, because the right words depend on the models a person uses, and the owner may want them shorter, stricter ("only when asked"), or gone.

## What it costs

- About 150 words in every chat call, also in calls where no visual could help: a background task's, a sub-agent's, a wiki job's. They are in the cached start, so a provider that caches charges them at its cached rate after the first call.
- One assertion in the chats lane's `internal/turn/context_test.go`, which compared the first system block with the standing text alone, now compares it with the standing text and these words.
- A person who empties the words still has blocks drawn if a model writes them; a person who wants none drawn switches `visuals.draw` off.
- A change to the chart format (decision [0540](0540-a-chart-is-a-small-json-object-in-a-fenced-chart-block.md)) is a change to this setting's default too, and a person who has their own words keeps the old ones.
