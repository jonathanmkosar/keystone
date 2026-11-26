# 0381. Text is read with the standard library, and a PDF is sent whole, its text not read

November 25, 2026 · decided in P9-02

## The question

The item asks for text extraction from the formats that can be read with the standard library or dependencies the repo already has, and PDF if a pure-Go reader is already a dependency. The repo depends on `golang.org/x/sys` and `modernc.org/sqlite` and nothing that reads HTML or PDF. What is read, how, and what happens to a PDF?

## The decision

- **Plain text, Markdown, code, CSV, TSV, JSON, YAML and the rest of `uploads.text_extensions`** are read as they are: UTF-8 with no zero bytes, a byte order mark dropped, line ends made `\n`. A file that is not UTF-8 is refused with the reason.
- **HTML** is read by a small reader on the standard library in `internal/uploads/extract.go`: tags taken out, scripts, styles, comments and a few other elements dropped with their content, block elements and line breaks starting lines, list items with a dash, table cells parted by a bar, character references decoded with `html.UnescapeString`. What it cannot read as a tag it keeps as text.
- **No PDF reader is added.** A PDF is stored, listed and shown, and no text is read from it. It is sent whole, as a file, to a model whose registry entry says `files` (each adapter already sends a PDF in its provider's form), and to any other model a note says it is there and why it is not shown. This part is provisional.

## Why

- Reading text from a PDF well means fonts, encodings and compressed streams. A small reader written here would read some PDFs and quietly miss others, and a new dependency needs a decision of its own and a look at its licence and upkeep. Neither belongs in this item.
- The models most likely to be given a PDF, Anthropic's and OpenAI's, read PDFs themselves, so sending the file whole gives a better reading than a home-made extraction would.
- The HTML reader is small enough to read in a minute and to test fully, and the HTML a person attaches is mostly a saved page or an export, where tags, scripts and styles are what must go.

## What it costs

- A model without `files` gets nothing of a PDF but its name. A pure-Go reader, or a local extraction tool, would close that; it is listed as provisional in the item.
- The HTML reader keeps no links' addresses and loses the layout of `pre` blocks.
