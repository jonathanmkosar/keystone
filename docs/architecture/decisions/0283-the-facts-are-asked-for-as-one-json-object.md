# 0283. The facts are asked for as one JSON object, and the answer is checked on the way in

November 25, 2026 · decided in P6-06

## The question

The memory pass asks a model for the durable facts of an exchange as structured output. The model seam has no response format: a request cannot ask a provider for JSON that fits a schema. A tool the model must call would do it for some providers, but the seam cannot force a tool call, and a small local model may not call tools well. How are the facts asked for and read?

## The decision

**In words, as one JSON object.** The prompt, `knowledge.memory_prompt`, asks for one JSON object and nothing else, `{"facts": ["...", "..."]}`, at most `knowledge.memory_max_facts` of them, and `{"facts": []}` when the exchange holds none.

**Checked on the way in.** `memory.Facts` reads the text from the first `{` to the last `}`, so a fenced block or a sentence around it does no harm. It must be an object with a `facts` list, each fact a string or an object with a `text`. Anything else fails the pass, with its reason, and adds nothing. Blank facts, facts longer than `knowledge.memory_fact_max_chars`, facts said twice, and facts past the limit are left out and counted.

**The facts are plain sentences about "the person"**, each standing on its own, so a source read later, by the person or by the wiki upkeep jobs, needs no context.

## Why

- Every adapter and every model can answer in text. JSON in text is what local models are commonly tuned for, and the owner's memory model is local.
- Reading the object out of the text costs nothing and forgives the commonest slips, fences and a line before the answer, while refusing anything whose shape is wrong. A pass that fails says so; it never stores a guess.
- A response format belongs in the model seam, for all three adapters at once, and is the models lane's to add. This item does not need it to be correct.

## What it costs

- A model that ignores the instruction fails the pass; the facts of that turn are lost, and the reason is on the pass.
- An answer cut off at `knowledge.memory_max_output_tokens` does not read and fails the pass.
- When the seam gains a response format, the request should ask for it, and the check stays as it is.
