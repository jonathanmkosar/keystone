# 0252. The web tools are off in a chat held for sensitive content

November 25, 2026 · decided in P7-03

## The question

A chat that was given content from a sensitive space is held to models that may read it (decision 0211), usually a local one. That model can still call `web_search`, whose query goes to Tavily, or `web_fetch`, whose address goes to a site. Either could carry words from the sensitive space out of Keystone. Fixed rule 3 names models, and Tavily is not one. Should the web tools run in such a chat?

## The decision

**No, until the owner says otherwise.** In a chat that `recall.Held` says is held, both tools answer, as an error result the model reads:

> Web search is off in this chat: it was given content from a sensitive space, and a search sends its words outside Keystone. A new chat can search the web.

`web_fetch` says the same of reading web pages. Nothing is sent. A chat whose mark cannot be read counts as held, as in decision 0211. The screen labels the row "off in this chat".

## Why

- The rule is about content leaving for places that may not hold it. A search query is content, and Tavily is a hosted service the registry does not class. Keeping the query in is the same caution as keeping the call in.
- New Keystone asked before a search whose query looked private. Keystone cannot ask yet (the approval policy is P7-02), so the safe side is to refuse; a person who wants a web search opens a new chat.
- It is three lines in the tools and costs nothing in chats that are not held.

## What it costs

- A person who recalled something sensitive and then wants a web search in the same chat cannot have one. The question is in the journal, agents 003, with the recommended answer: once approvals exist, ask in a held chat instead of refusing.
- The tools read the hold on every call, which is one indexed query.
