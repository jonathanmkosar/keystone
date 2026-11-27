# Systemone exchanges

Test data. Everything in this folder is invented: the models, the addresses, the states, the numbers and the token counts. The file says so itself with `"test_data": true`.

`exchanges.json` holds hand-written exchanges for the systemone adapter (P14-02), in the shape Ollama's decision API, `POST /v1/systemone`, reads and answers. The tests in `internal/adapters/systemone` replay them from a server on this machine. No real endpoint has been called; the live check waits on the owner's Ollama 0.35.1 or later with a decision model pulled.

| Part | Shows |
| --- | --- |
| `exchanges` | The router's two questions (a step and an effort) on a local Ollama with no key, and two yes or no questions on an endpoint with a key. Each has the seam's request, the body sent (`sent`, with `sent_order` for the order its questions are written in), the endpoint's answer and the seam's result. |
| `refusals` | A status and body from the endpoint, and the seam error kind it becomes. |
| `malformed` | Answers that do not answer the request `{step: choice of a or b, check: yes or no}`, each refused. |

The format is described in [`docs/architecture/adapter-systemone.md`](../../docs/architecture/adapter-systemone.md). Use invented content only.
