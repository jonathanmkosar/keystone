# 0382. A turn's image asks its route for vision, and a call is readied for the least of its route

November 25, 2026 · decided in P9-02

## The question

An image goes only to a model whose registry entry says it can see. When the chat's model cannot, the turn uses the first fallback that can, or, if none can, leaves the image out with a notice to the person. How does a turn find its route, what is a call given when part of its route cannot see, and which model is the chat's afterwards?

## The decision

**The route asks for vision** (`routing.Ask.Needs`) when the turn's own message holds an image, and when an earlier message of its branch does and the chat's model can see. `routing.Plan` then leaves out every model without vision, so the route starts at the chat's model if it can see and otherwise at the first model of the fallback chain that can. The fallback chain is used for this even when `routing.max_fallbacks` is 0: the route is still one model.

**When no model can take the call with vision**, it is planned as before, and its images are left out.

**A call is readied for the least of its route** (`uploads.Prepare`): an image goes as its bytes only when every model the call can reach can see; otherwise a note (`uploads.image_withheld_note`) stands in its place. The same holds for a document and the `files` capability. This is the same rule as for sensitive content (decision 0323): a call is built once and sent to whichever model of its route answers.

**The person is told** about the image of their own message that a reply did not see, and about a reply a fallback wrote to see one: notices on the reply, kept in `upload_calls`. An earlier image left out of a later call is recorded in the inspector, with no notice.

**The chat keeps the person's model.** A turn that went to a fallback only to see its image writes the chat's own model on its row, so the next turn goes back to it.

## Why

- Asking the route reuses `Plan`'s capability filter, which was there for this, and keeps the order the person set for the fallback chain.
- Asking for vision for an earlier image when the chat's model can see keeps a follow-up question about the picture with a model that saw it, instead of a fallback that cannot see. When the chat's model cannot see, an earlier image does not move every later turn to a fallback: the person picked that model.
- Building for the least of the route is the second lock: whatever the route, no model without vision is sent an image, because the call cannot know which model of the route will answer.
- A person who picked a model should keep it; the fallback answered one message for a reason that message alone had.

## What it costs

- A turn about an earlier image, on a model that can see, has no fallback that cannot see; if the model is down, the turn fails instead of going to one that would answer blind.
- A route with a model that cannot see anywhere in it sends no image, even to a first model that could. The route already leaves such models out when the turn has an image, so this bites only when vision could not be asked for at all.
- The model picker shows the chat's model after a fallback answered, and the reply's footnote names the fallback; the notice says why.
