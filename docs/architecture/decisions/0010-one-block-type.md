# 0010. A content block is one struct with a type field

November 24, 2026 · decided in P3-01

## The question

The model seam's format has six kinds of content block and six kinds of stream event. In Go there are two ways to write that. One is an interface with a struct for each kind. The other is a single struct with a `type` field and the fields of every kind side by side.

## The decision

`Block` is one struct, and so is `Event`. The `type` field says which of the other fields are used. `ValidateBlock` and `ValidateEvent` refuse a value with a field that does not belong to its type, and the `Encode` and `Decode` functions always validate.

The reasons:

- It reads and writes as JSON with the standard library alone. An interface needs a hand-written decoder that looks at `type` first, for blocks, for the blocks inside tool results, and for events.
- Values compare and copy as plain data, which keeps tests and fixtures short.
- The project prefers plain functions and data over type hierarchies.

## What it costs

The compiler does not stop a text block from carrying a tool name. Validation does, at run time, so code that builds a block by hand and skips the `Encode` functions must call `Validate` itself. A new kind of block adds fields to a struct every block carries. With six kinds that is cheap.

If the kinds multiply, or each grows many fields of its own, this is worth another look. The JSON would not change, so stored chats would still read.
